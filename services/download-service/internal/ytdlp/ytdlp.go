// Package ytdlp runs yt-dlp in two passes (spec §5.3): metadata first
// (--dump-single-json, written to a file) and then the chosen format from that
// file (--load-info-json), either to a temp file (merges) or piped to stdout.
// Arguments are passed without a shell; --no-check-certificates is never used.
package ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
)

const progressTemplate = "download:TNPROG %(progress.downloaded_bytes)s %(progress.total_bytes)s %(progress.total_bytes_estimate)s %(progress.speed)s %(progress.eta)s"

// Error is a failed yt-dlp run, classified for the retry strategy.
type Error struct {
	Kind domain.ErrorKind
	Msg  string // last ERROR line (or stderr tail)
}

func (e *Error) Error() string { return fmt.Sprintf("yt-dlp %s: %s", e.Kind, e.Msg) }

// Opts are per-attempt network settings.
type Opts struct {
	Proxy       string
	CookiesFile string
	UserAgent   string
	MaxFilesize int64
	Metadata    bool // probe: short socket timeout, no yt-dlp retries
}

// Runner executes the yt-dlp binary.
type Runner struct {
	Bin        string
	JSRuntimes string // e.g. "deno" (YouTube signature solving)
	FFmpeg     string // --ffmpeg-location
}

func (r *Runner) base(o Opts) []string {
	socket, retries := "30", "3"
	if o.Metadata {
		// The chat is sitting on «проверяю». Metadata must return before the
		// long download timeouts; a bot wall or a dead source shows up in seconds.
		socket, retries = "8", "0"
	}
	args := []string{"--ignore-config", "--no-playlist", "--no-color", "--socket-timeout", socket, "--retries", retries, "--fragment-retries", retries}
	if r.JSRuntimes != "" {
		args = append(args, "--js-runtimes", r.JSRuntimes)
	}
	if r.FFmpeg != "" {
		args = append(args, "--ffmpeg-location", r.FFmpeg)
	}
	if o.Proxy != "" {
		args = append(args, "--proxy", o.Proxy)
	}
	if o.CookiesFile != "" {
		args = append(args, "--cookies", o.CookiesFile)
	}
	if o.UserAgent != "" {
		args = append(args, "--user-agent", o.UserAgent)
	}
	if o.MaxFilesize > 0 {
		args = append(args, "--max-filesize", strconv.FormatInt(o.MaxFilesize, 10))
	}
	return args
}

// command builds the exec.Cmd in its own process group: on cancel the whole
// group (yt-dlp + ffmpeg children) is killed and pipes are released.
func (r *Runner) command(ctx context.Context, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.Bin, args...) //nolint:gosec // fixed binary, no shell; url is normalized
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// Probe is pass 1: metadata only. The raw JSON is written to infoPath for pass 2.
func (r *Runner) Probe(ctx context.Context, url string, o Opts, infoPath string) (domain.Info, error) {
	o.Metadata = true
	args := append(r.base(o), "--skip-download", "--dump-single-json", "--", url)
	cmd := r.command(ctx, args)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &limited{max: 64 << 10, buf: &stderr}
	if err := cmd.Run(); err != nil {
		return domain.Info{}, runErr(ctx, err, stderr.String())
	}
	var info domain.Info
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return domain.Info{}, &Error{Kind: domain.KindInternal, Msg: "bad metadata json"}
	}
	if err := os.WriteFile(infoPath, stdout.Bytes(), 0o600); err != nil {
		return domain.Info{}, fmt.Errorf("write info json: %w", err)
	}
	return info, nil
}

// DownloadFile is pass 2 into dir (merges need a seekable file). Returns the final path.
func (r *Runner) DownloadFile(ctx context.Context, infoPath, spec, dir string, o Opts, onProgress func(Progress)) (string, error) {
	args := append(r.base(o), "--load-info-json", infoPath, "-f", spec,
		"--merge-output-format", "mp4", "--postprocessor-args", "Merger+ffmpeg_o:-movflags +faststart",
		"-P", dir, "-o", "%(id)s.%(ext)s", "--no-simulate", "--print", "after_move:TNFILE %(filepath)s",
		"--newline", "--progress", "--progress-template", progressTemplate, "--no-mtime")
	cmd := r.command(ctx, args)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &limited{max: 64 << 10, buf: &stderr}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start yt-dlp: %w", err)
	}
	var path string
	tr := newTracker(onProgress)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if p, ok := strings.CutPrefix(line, "TNFILE "); ok {
			path = strings.TrimSpace(p)
			continue
		}
		tr.line(line)
	}
	if err := cmd.Wait(); err != nil {
		return "", runErr(ctx, err, stderr.String())
	}
	if path == "" || !strings.HasPrefix(filepath.Clean(path), filepath.Clean(dir)+string(os.PathSeparator)) {
		return "", &Error{Kind: domain.KindInternal, Msg: "no output file"}
	}
	return path, nil
}

// Stream is pass 2 for a single progressive format: bytes come on the returned
// reader (stdout) and go straight to S3; wait() reports the classified result
// and must be called after the reader is drained.
func (r *Runner) Stream(ctx context.Context, infoPath, spec string, o Opts, onProgress func(Progress)) (io.ReadCloser, func() error, error) {
	args := append(r.base(o), "--load-info-json", infoPath, "-f", spec, "-o", "-",
		"--newline", "--progress", "--progress-template", progressTemplate)
	cmd := r.command(ctx, args)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start yt-dlp: %w", err)
	}
	var tail bytes.Buffer
	lim := &limited{max: 64 << 10, buf: &tail}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // progress is on stderr when stdout carries the file
		defer wg.Done()
		tr := newTracker(onProgress)
		sc := bufio.NewScanner(stderrPipe)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			if !tr.line(sc.Text()) {
				_, _ = lim.Write(append(sc.Bytes(), '\n'))
			}
		}
	}()
	wait := func() error {
		wg.Wait()
		if err := cmd.Wait(); err != nil {
			return runErr(ctx, err, tail.String())
		}
		return nil
	}
	return stdout, wait, nil
}

func runErr(ctx context.Context, err error, stderr string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return fmt.Errorf("run yt-dlp: %w", err)
	}
	return &Error{Kind: domain.Classify(stderr), Msg: lastError(stderr)}
}

func lastError(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "ERROR:") {
			return truncate(strings.TrimSpace(lines[i]), 500)
		}
	}
	if len(lines) > 0 {
		return truncate(lines[len(lines)-1], 500)
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// limited keeps the last max bytes written (stderr tail).
type limited struct {
	max int
	buf *bytes.Buffer
	mu  sync.Mutex
}

func (l *limited) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf.Write(p)
	if over := l.buf.Len() - l.max; over > 0 {
		l.buf.Next(over)
	}
	return len(p), nil
}
