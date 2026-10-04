// Package ffmpeg turns a validated recipe into ffmpeg argv and runs it.
// Arguments are passed to exec directly — never through a shell.
package ffmpeg

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tapenest/tapenest/services/video-editor-service/internal/domain"
)

// Step is one ffmpeg invocation. Files are written before the process starts.
type Step struct {
	Name  string
	Args  []string
	Files map[string]string
}

// Input is everything Build needs. Paths are local temp files.
type Input struct {
	Source   string
	HasAudio bool
	Music    string
	Out      string
	WorkDir  string
	Font     string
	Width    int
	Height   int
	Recipe   domain.Recipe
}

// Build plans the export: one file per clip, then join, captions, music bed.
func Build(in Input) ([]Step, error) {
	if in.Width < 2 || in.Height < 2 {
		in.Width, in.Height = 1280, 720
	}
	in.Width, in.Height = in.Width&^1, in.Height&^1
	if len(in.Recipe.Clips) == 0 {
		return nil, fmt.Errorf("recipe has no clips")
	}
	var steps []Step
	segs := make([]string, len(in.Recipe.Clips))
	for i, c := range in.Recipe.Clips {
		segs[i] = filepath.Join(in.WorkDir, fmt.Sprintf("seg-%02d.mp4", i))
		steps = append(steps, clipStep(in, c, segs[i]))
	}
	joined := filepath.Join(in.WorkDir, "joined.mp4")
	steps = append(steps, joinStep(in.Recipe, segs, joined))
	current := joined
	if len(in.Recipe.Texts) > 0 {
		captioned := filepath.Join(in.WorkDir, "captioned.mp4")
		steps = append(steps, textStep(in, current, captioned))
		current = captioned
	}
	if in.Music != "" {
		steps = append(steps, musicStep(in, current, in.Out))
		return steps, nil
	}
	steps[len(steps)-1] = retarget(steps[len(steps)-1], current, in.Out)
	return steps, nil
}

func clipStep(in Input, c domain.Clip, out string) Step {
	vol := c.Volume
	if in.Recipe.MuteSource {
		vol = 0
	}
	vchain := videoChain(c, in.Width, in.Height)
	achain := audioChain(vol, c.Speed)
	args := []string{"-y", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-ss", num(c.StartSec), "-to", num(c.EndSec), "-i", in.Source}
	var graph string
	if in.HasAudio {
		graph = fmt.Sprintf("[0:v]%s[v];[0:a]%s[a]", vchain, achain)
	} else {
		outDur := (c.EndSec - c.StartSec) / c.Speed
		args = append(args, "-f", "lavfi", "-t", num(outDur), "-i", "anullsrc=channel_layout=stereo:sample_rate=48000")
		graph = fmt.Sprintf("[0:v]%s[v];[1:a]%s[a]", vchain, achain)
	}
	args = append(args, "-filter_complex", graph, "-map", "[v]", "-map", "[a]",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-ar", "48000", "-ac", "2", "-movflags", "+faststart", out)
	return Step{Name: "clip", Args: args}
}

func videoChain(c domain.Clip, w, h int) string {
	var f []string
	if c.Crop.X > 0.001 || c.Crop.Y > 0.001 || c.Crop.W < 0.999 || c.Crop.H < 0.999 {
		f = append(f, fmt.Sprintf("crop=trunc(iw*%s/2)*2:trunc(ih*%s/2)*2:min(trunc(iw*%s)\\,iw-2):min(trunc(ih*%s)\\,ih-2)",
			num(c.Crop.W), num(c.Crop.H), num(c.Crop.X), num(c.Crop.Y)))
	}
	switch c.Rotate {
	case 90:
		f = append(f, "transpose=1")
	case 180:
		f = append(f, "transpose=1,transpose=1")
	case 270:
		f = append(f, "transpose=2")
	}
	if c.Speed != 1 {
		f = append(f, "setpts="+num(1/c.Speed)+"*PTS")
	}
	f = append(f, fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:black,setsar=1,fps=30,format=yuv420p", w, h, w, h))
	return strings.Join(f, ",")
}

func audioChain(volume, speed float64) string {
	parts := []string{"volume=" + num(volume)}
	if speed != 1 {
		parts = append(parts, atempo(speed)...)
	}
	parts = append(parts, "aformat=sample_rates=48000:channel_layouts=stereo")
	return strings.Join(parts, ",")
}

// atempo accepts 0.5..2, so extreme speeds are chained.
func atempo(speed float64) []string {
	var parts []string
	s := speed
	for s > 2.0001 {
		parts = append(parts, "atempo=2")
		s /= 2
	}
	for s < 0.4999 {
		parts = append(parts, "atempo=0.5")
		s /= 0.5
	}
	parts = append(parts, "atempo="+num(s))
	return parts
}

func joinStep(r domain.Recipe, segs []string, out string) Step {
	if len(segs) == 1 || allHardCuts(r) {
		var b strings.Builder
		for _, s := range segs {
			fmt.Fprintf(&b, "file '%s'\n", strings.ReplaceAll(s, "'", `'\''`))
		}
		list := filepath.Join(filepath.Dir(out), "list.txt")
		return Step{Name: "concat", Files: map[string]string{list: b.String()}, Args: []string{
			"-y", "-nostdin", "-hide_banner", "-loglevel", "error",
			"-f", "concat", "-safe", "0", "-i", list, "-c", "copy", "-movflags", "+faststart", out,
		}}
	}
	args := []string{"-y", "-nostdin", "-hide_banner", "-loglevel", "error"}
	for _, s := range segs {
		args = append(args, "-i", s)
	}
	args = append(args, "-filter_complex", xfadeGraph(r), "-map", "[v]", "-map", "[a]",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-ar", "48000", "-ac", "2", "-movflags", "+faststart", out)
	return Step{Name: "xfade", Args: args}
}

func allHardCuts(r domain.Recipe) bool {
	for i, c := range r.Clips {
		if i > 0 && c.Transition != "none" {
			return false
		}
	}
	return true
}

func xfadeGraph(r domain.Recipe) string {
	var b strings.Builder
	vPrev, aPrev := "[0:v]", "[0:a]"
	elapsed := (r.Clips[0].EndSec - r.Clips[0].StartSec) / r.Clips[0].Speed
	consumed := 0.0
	for i := 1; i < len(r.Clips); i++ {
		c := r.Clips[i]
		name := c.Transition
		dur := 0.04
		if name != "none" {
			name = c.Transition
			dur = c.TransitionSec
		} else {
			name = "fade"
		}
		offset := elapsed - consumed - dur
		if offset < 0 {
			offset = 0
		}
		vNext := fmt.Sprintf("[v%d]", i)
		aNext := fmt.Sprintf("[a%d]", i)
		if i == len(r.Clips)-1 {
			vNext, aNext = "[v]", "[a]"
		}
		fmt.Fprintf(&b, "%s[%d:v]xfade=transition=%s:duration=%s:offset=%s%s;",
			vPrev, i, name, num(dur), num(offset), vNext)
		fmt.Fprintf(&b, "%s[%d:a]acrossfade=d=%s:c1=tri:c2=tri%s", aPrev, i, num(dur), aNext)
		if i != len(r.Clips)-1 {
			b.WriteByte(';')
		}
		vPrev, aPrev = vNext, aNext
		elapsed += (c.EndSec - c.StartSec) / c.Speed
		consumed += dur
	}
	return b.String()
}

func textStep(in Input, src, out string) Step {
	var filters []string
	for _, t := range in.Recipe.Texts {
		filters = append(filters, fmt.Sprintf(
			"drawtext=fontfile='%s':text='%s':fontsize=%d:fontcolor=%s:x=(w*%s-text_w/2):y=(h*%s-text_h/2):enable='between(t,%s,%s)'",
			escapePath(in.Font), escapeText(strings.TrimSpace(t.Text)), t.Size, t.Color,
			num(t.X), num(t.Y), num(t.StartSec), num(t.EndSec)))
	}
	return Step{Name: "text", Args: []string{
		"-y", "-nostdin", "-hide_banner", "-loglevel", "error", "-i", src,
		"-vf", strings.Join(filters, ","),
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p",
		"-c:a", "copy", "-movflags", "+faststart", out,
	}}
}

func musicStep(in Input, src, out string) Step {
	return Step{Name: "music", Args: []string{
		"-y", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", src, "-stream_loop", "-1", "-i", in.Music,
		"-filter_complex", fmt.Sprintf("[1:a]volume=%s,aformat=sample_rates=48000:channel_layouts=stereo[m];[0:a][m]amix=inputs=2:duration=first:dropout_transition=0:normalize=0[a]", num(in.Recipe.MusicVolume)),
		"-map", "0:v", "-map", "[a]", "-c:v", "copy", "-c:a", "aac", "-shortest", "-movflags", "+faststart", out,
	}}
}

// retarget points the last step at the final output path.
func retarget(s Step, from, to string) Step {
	if from == to {
		return s
	}
	args := append([]string(nil), s.Args...)
	if n := len(args); n > 0 && args[n-1] == from {
		args[n-1] = to
	}
	s.Args = args
	return s
}

func num(v float64) string {
	return strconv.FormatFloat(v, 'f', 4, 64)
}

func escapeText(s string) string {
	return strings.NewReplacer(`\`, `\\`, `:`, `\:`, `'`, `\'`, `%`, `\%`).Replace(s)
}

func escapePath(s string) string {
	return strings.NewReplacer(`\`, `\\`, `:`, `\:`, `'`, `\'`).Replace(s)
}
