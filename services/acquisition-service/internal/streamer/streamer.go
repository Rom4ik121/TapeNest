// Package streamer serves an audio file straight out of an in-progress torrent
// (TorrServer-like): HTTP Range over the file's byte span, blocking until the
// pieces covering the requested bytes are downloaded and hash-checked. The
// wanted file is downloaded first (max file priority + sequential pieces), so
// playback starts after the first few hundred KB instead of after the album.
package streamer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tapenest/tapenest/services/acquisition-service/internal/match"
	"github.com/tapenest/tapenest/services/acquisition-service/internal/qbt"
)

// Errors.
var (
	ErrNotReady = errors.New("stream: file not located yet")
	ErrStalled  = errors.New("stream: download stalled")
	ErrRange    = errors.New("stream: unsatisfiable range")
)

// Source is the qBittorrent subset the streamer needs.
type Source interface {
	Torrent(ctx context.Context, hash string) (qbt.Torrent, error)
	Files(ctx context.Context, hash string) ([]qbt.File, error)
	PieceSize(ctx context.Context, hash string) (int64, error)
	PieceStates(ctx context.Context, hash string) ([]int, error)
}

// Layout locates one file inside the torrent's piece space.
type Layout struct {
	Name      string
	Size      int64
	Offset    int64 // of the file within the concatenated torrent data
	PieceSize int64
	Progress  float64
}

// Locate computes the file layout (files are concatenated in index order).
func Locate(ctx context.Context, src Source, hash string, index int) (Layout, error) {
	files, err := src.Files(ctx, hash)
	if err != nil {
		return Layout{}, err
	}
	ps, err := src.PieceSize(ctx, hash)
	if err != nil {
		return Layout{}, err
	}
	if ps <= 0 {
		return Layout{}, ErrNotReady
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Index < files[j].Index })
	var off int64
	for _, f := range files {
		if f.Index == index {
			return Layout{Name: f.Name, Size: f.Size, Offset: off, PieceSize: ps, Progress: f.Progress}, nil
		}
		off += f.Size
	}
	return Layout{}, ErrNotReady
}

// Available returns how many contiguous bytes of the file are downloaded starting at from.
func Available(states []int, l Layout, from int64) int64 {
	if from >= l.Size || l.PieceSize <= 0 {
		return 0
	}
	abs := l.Offset + from
	end := l.Offset + l.Size // exclusive
	p := abs / l.PieceSize
	var got int64
	for p < int64(len(states)) && states[p] == 2 {
		pieceEnd := (p + 1) * l.PieceSize
		if pieceEnd >= end {
			return l.Size - from
		}
		got = pieceEnd - abs
		p++
	}
	return got
}

// Streamer serves files.
type Streamer struct {
	Src       Source
	PollEvery time.Duration
	Stall     time.Duration // give up when no new bytes arrive for this long
	Chunk     int64
}

// New builds a streamer with defaults.
func New(src Source) *Streamer {
	return &Streamer{Src: src, PollEvery: 250 * time.Millisecond, Stall: 60 * time.Second, Chunk: 256 << 10}
}

// resolve finds the file on disk (incomplete dir while downloading, save dir after).
func (s *Streamer) resolve(ctx context.Context, hash string, l Layout) (string, error) {
	t, err := s.Src.Torrent(ctx, hash)
	if err != nil {
		return "", err
	}
	for _, dir := range []string{t.DownloadPath, t.SavePath} {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(l.Name))
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", ErrNotReady
}

// ParseRange parses a single "bytes=a-b" range (suffix ranges included).
func ParseRange(h string, size int64) (start, end int64, partial bool, err error) {
	if h == "" {
		return 0, size - 1, false, nil
	}
	spec, ok := strings.CutPrefix(h, "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return 0, 0, false, ErrRange
	}
	a, b, _ := strings.Cut(spec, "-")
	switch {
	case a == "" && b != "":
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false, ErrRange
		}
		start, end = max(size-n, 0), size-1
	default:
		start, err = strconv.ParseInt(a, 10, 64)
		if err != nil || start < 0 {
			return 0, 0, false, ErrRange
		}
		end = size - 1
		if b != "" {
			if end, err = strconv.ParseInt(b, 10, 64); err != nil {
				return 0, 0, false, ErrRange
			}
		}
		end = min(end, size-1)
	}
	if start >= size || start > end {
		return 0, 0, false, ErrRange
	}
	return start, end, true, nil
}

// Serve streams file `index` of torrent `hash`.
func (s *Streamer) Serve(w http.ResponseWriter, r *http.Request, hash string, index int) error {
	ctx := r.Context()
	l, err := Locate(ctx, s.Src, hash, index)
	if err != nil {
		return err
	}
	start, end, partial, err := ParseRange(r.Header.Get("Range"), l.Size)
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", l.Size))
		return ErrRange
	}
	h := w.Header()
	h.Set("Content-Type", match.ContentType(l.Name))
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, l.Size))
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(status)
		return nil
	}
	var f *os.File
	defer func() {
		if f != nil {
			f.Close()
		}
	}()
	flusher := http.NewResponseController(w)
	wrote := false
	pos := start
	lastProgress := time.Now()
	buf := make([]byte, s.Chunk)
	for pos <= end {
		states, err := s.Src.PieceStates(ctx, hash)
		if err != nil {
			return s.abort(w, wrote, err)
		}
		avail := Available(states, l, pos)
		if avail == 0 {
			if time.Since(lastProgress) > s.Stall {
				return s.abort(w, wrote, ErrStalled)
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(s.PollEvery):
			}
			continue
		}
		if f == nil {
			p, err := s.resolve(ctx, hash, l)
			if err != nil {
				return s.abort(w, wrote, err)
			}
			if f, err = os.Open(p); err != nil { //nolint:gosec // path comes from qBittorrent, not the client
				return s.abort(w, wrote, err)
			}
		}
		for avail > 0 && pos <= end {
			n := min(avail, end-pos+1, int64(len(buf)))
			m, err := f.ReadAt(buf[:n], pos)
			if m > 0 {
				if !wrote {
					w.WriteHeader(status)
					wrote = true
				}
				if _, werr := w.Write(buf[:m]); werr != nil {
					return nil // client went away
				}
				_ = flusher.Flush()
				pos += int64(m)
				avail -= int64(m)
				lastProgress = time.Now()
			}
			if err != nil && !errors.Is(err, io.EOF) {
				// the file moved (temp → save dir on completion): reopen next round
				f.Close()
				f = nil
				break
			}
			if m == 0 {
				break
			}
		}
	}
	return nil
}

func (s *Streamer) abort(w http.ResponseWriter, wrote bool, err error) error {
	if wrote {
		return nil // headers are gone; the client sees a short body and retries with Range
	}
	for _, k := range []string{"Content-Length", "Content-Range", "Content-Type", "Accept-Ranges"} {
		w.Header().Del(k)
	}
	return err
}
