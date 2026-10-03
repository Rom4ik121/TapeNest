package ffmpeg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ProbeResult is what the editor needs from the source file.
type ProbeResult struct {
	Duration float64
	Width    int
	Height   int
	HasAudio bool
}

// Probe reads duration, size and whether an audio stream exists.
func Probe(ctx context.Context, bin, path string) (ProbeResult, error) {
	cmd := exec.CommandContext(ctx, bin, //nolint:gosec // bin is config, path is a temp file we created
		"-v", "error", "-show_entries", "format=duration:stream=codec_type,width,height", "-of", "json", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return ProbeResult{}, fmt.Errorf("ffprobe: %w: %s", err, tail(stderr.Bytes()))
	}
	var raw struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return ProbeResult{}, fmt.Errorf("ffprobe json: %w", err)
	}
	var out ProbeResult
	for _, s := range raw.Streams {
		if s.CodecType == "audio" {
			out.HasAudio = true
		}
		if s.CodecType == "video" && s.Width > 0 {
			out.Width, out.Height = s.Width, s.Height
		}
	}
	if _, err := fmt.Sscanf(strings.TrimSpace(raw.Format.Duration), "%f", &out.Duration); err != nil || out.Duration <= 0 {
		return ProbeResult{}, errors.New("ffprobe: missing duration")
	}
	return out, nil
}

// Run writes any sidecar files and executes each step in order.
func Run(ctx context.Context, bin string, steps []Step) error {
	for _, s := range steps {
		for path, body := range s.Files {
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				return fmt.Errorf("%s: write list: %w", s.Name, err)
			}
		}
		cmd := exec.CommandContext(ctx, bin, s.Args...) //nolint:gosec // argv is built in this package, not a shell
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w: %s", s.Name, err, tail(stderr.Bytes()))
		}
	}
	return nil
}

func tail(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 240 {
		s = s[len(s)-240:]
	}
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
