package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ComposeClip is one piece of a timeline already copied to a local file.
// Transition is how this clip arrives from the previous one (ignored on the first).
type ComposeClip struct {
	Path          string
	InSec         float64
	OutSec        float64
	Speed         float64
	Volume        float64
	CropX         float64
	CropY         float64
	CropW         float64
	CropH         float64
	Rotate        int
	Transition    string
	TransitionSec float64
}

// ComposeText is burned in for [StartSec, EndSec). Text is the overlay; TextFile is filled while rendering.
type ComposeText struct {
	Text     string
	TextFile string
	StartSec float64
	EndSec   float64
	X        float64
	Y        float64
}

// ComposeMusic is an extra audio bed mixed under the clips.
type ComposeMusic struct {
	Path      string
	InSec     float64
	Volume    float64
	OffsetSec float64
}

// ComposeSpec is a finished timeline. The service has already checked ownership and ranges.
type ComposeSpec struct {
	Clips []ComposeClip
	Texts []ComposeText
	Music *ComposeMusic
}

// Compose renders spec to dst (mp4) with the ffmpeg already used for trimming.
func (f *FFmpeg) Compose(ctx context.Context, spec ComposeSpec, dst string) error {
	if len(spec.Clips) == 0 {
		return fmt.Errorf("ffmpeg: empty timeline")
	}
	font := ""
	if len(spec.Texts) > 0 {
		font = fontFile()
		if font == "" {
			return fmt.Errorf("ffmpeg: no font for text")
		}
		if err := writeTexts(filepath.Dir(dst), spec.Texts); err != nil {
			return err
		}
	}
	clipAudio := make([]bool, len(spec.Clips))
	for i, clip := range spec.Clips {
		clipAudio[i] = f.hasAudio(ctx, clip.Path)
	}
	musicAudio := false
	if spec.Music != nil {
		musicAudio = f.hasAudio(ctx, spec.Music.Path)
		if !musicAudio {
			return fmt.Errorf("ffmpeg: music has no audio")
		}
	}
	args, err := composeArgs(spec, dst, font, clipAudio, musicAudio)
	if err != nil {
		return err
	}
	return f.run(ctx, args...)
}

func writeTexts(dir string, texts []ComposeText) error {
	for i := range texts {
		path := filepath.Join(dir, fmt.Sprintf("overlay-%d.txt", i))
		if err := os.WriteFile(path, []byte(texts[i].Text), 0o600); err != nil {
			return err
		}
		texts[i].TextFile = path
	}
	return nil
}

func fontFile() string {
	for _, path := range []string{
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
	} {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			return path
		}
	}
	return ""
}

func (f *FFmpeg) hasAudio(ctx context.Context, path string) bool {
	cmd := exec.CommandContext(ctx, probeBin(f.Bin), "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=index", "-of", "csv=p=0", path)
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func probeBin(ffmpegBin string) string {
	name := "ffprobe"
	if strings.Contains(filepath.Base(ffmpegBin), "ffmpeg") {
		name = strings.Replace(filepath.Base(ffmpegBin), "ffmpeg", "ffprobe", 1)
	}
	candidate := filepath.Join(filepath.Dir(ffmpegBin), name)
	if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
		return candidate
	}
	if p, err := exec.LookPath("ffprobe"); err == nil {
		return p
	}
	return name
}

func clipOutput(c ComposeClip) float64 {
	if c.Speed <= 0 {
		return 0
	}
	return (c.OutSec - c.InSec) / c.Speed
}

// logicalJoin is the overlap used for xfade. Hard cuts return 0; the graph then uses concat.
func logicalJoin(prev, cur ComposeClip) float64 {
	if cur.Transition == "" || cur.Transition == "none" {
		return 0
	}
	td := cur.TransitionSec
	if td <= 0 {
		td = 0.5
	}
	if td < 0.2 {
		td = 0.2
	}
	if td > 1.5 {
		td = 1.5
	}
	cap := clipOutput(prev)
	if other := clipOutput(cur); other < cap {
		cap = other
	}
	cap *= 0.45
	if td > cap {
		td = cap
	}
	if td < 0.05 {
		return 0
	}
	return td
}

func composeArgs(spec ComposeSpec, dst, font string, clipAudio []bool, musicAudio bool) ([]string, error) {
	graph, err := filterGraph(spec, font, clipAudio, musicAudio)
	if err != nil {
		return nil, err
	}
	args := []string{"-y"}
	for _, clip := range spec.Clips {
		args = append(args, "-i", clip.Path)
	}
	if spec.Music != nil {
		args = append(args, "-i", spec.Music.Path)
	}
	args = append(args,
		"-filter_complex", graph,
		"-map", "[vout]", "-map", "[aout]",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-movflags", "+faststart",
		dst,
	)
	return args, nil
}

func filterGraph(spec ComposeSpec, font string, clipAudio []bool, musicAudio bool) (string, error) {
	if len(clipAudio) != len(spec.Clips) {
		return "", fmt.Errorf("ffmpeg: audio flags")
	}
	var b strings.Builder
	for i, clip := range spec.Clips {
		if clip.Speed <= 0 || clip.OutSec <= clip.InSec {
			return "", fmt.Errorf("ffmpeg: bad clip %d", i)
		}
		fmt.Fprintf(&b, "[%d:v]trim=start=%.3f:end=%.3f,setpts=(PTS-STARTPTS)/%.3f,fps=30", i, clip.InSec, clip.OutSec, clip.Speed)
		fmt.Fprintf(&b, ",crop=w=iw*%.4f:h=ih*%.4f:x=iw*%.4f:y=ih*%.4f", clip.CropW, clip.CropH, clip.CropX, clip.CropY)
		switch clip.Rotate {
		case 90:
			b.WriteString(",transpose=1")
		case 180:
			b.WriteString(",hflip,vflip")
		case 270:
			b.WriteString(",transpose=2")
		case 0:
		default:
			return "", fmt.Errorf("ffmpeg: bad rotate %d", clip.Rotate)
		}
		b.WriteString(",scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2:color=0x16141C,setsar=1,format=yuv420p")
		fmt.Fprintf(&b, "[v%d];", i)
		dur := clipOutput(clip)
		if clipAudio[i] {
			fmt.Fprintf(&b, "[%d:a]aformat=sample_fmts=fltp:sample_rates=44100:channel_layouts=stereo,atrim=start=%.3f:end=%.3f,asetpts=PTS-STARTPTS", i, clip.InSec, clip.OutSec)
			if clip.Speed != 1 {
				fmt.Fprintf(&b, ",atempo=%.3f", clip.Speed)
			}
			fmt.Fprintf(&b, ",volume=%.3f[a%d];", clip.Volume, i)
		} else {
			fmt.Fprintf(&b, "anullsrc=r=44100:cl=stereo,atrim=0:%.3f,asetpts=PTS-STARTPTS,volume=%.3f[a%d];", dur, clip.Volume, i)
		}
	}

	vCur, aCur := "v0", "a0"
	elapsed := clipOutput(spec.Clips[0])
	useFade := false
	for i := 1; i < len(spec.Clips); i++ {
		if logicalJoin(spec.Clips[i-1], spec.Clips[i]) > 0 {
			useFade = true
			break
		}
	}
	if useFade {
		for i := 1; i < len(spec.Clips); i++ {
			td := logicalJoin(spec.Clips[i-1], spec.Clips[i])
			name := "fade"
			if td == 0 {
				td = 0.033
			}
			if spec.Clips[i].Transition == "wipe" && logicalJoin(spec.Clips[i-1], spec.Clips[i]) > 0 {
				name = "wipeleft"
			}
			offset := elapsed - td
			if offset < 0.01 {
				offset = 0.01
			}
			nextV := fmt.Sprintf("vx%d", i)
			nextA := fmt.Sprintf("ax%d", i)
			fmt.Fprintf(&b, "[%s][v%d]xfade=transition=%s:duration=%.3f:offset=%.3f[%s];", vCur, i, name, td, offset, nextV)
			fmt.Fprintf(&b, "[%s][a%d]acrossfade=d=%.3f:c1=tri:c2=tri[%s];", aCur, i, td, nextA)
			vCur, aCur = nextV, nextA
			elapsed = offset + clipOutput(spec.Clips[i])
		}
	} else if len(spec.Clips) > 1 {
		elapsed = 0
		for i, clip := range spec.Clips {
			fmt.Fprintf(&b, "[v%d][a%d]", i, i)
			elapsed += clipOutput(clip)
		}
		fmt.Fprintf(&b, "concat=n=%d:v=1:a=1[vx][ax];", len(spec.Clips))
		vCur, aCur = "vx", "ax"
	}

	if len(spec.Texts) > 0 {
		if font == "" {
			return "", fmt.Errorf("ffmpeg: no font for text")
		}
		for i, text := range spec.Texts {
			if text.TextFile == "" {
				return "", fmt.Errorf("ffmpeg: text file")
			}
			next := fmt.Sprintf("vt%d", i)
			fmt.Fprintf(&b, "[%s]drawtext=fontfile=%s:textfile=%s:fontsize=48:fontcolor=white:borderw=3:bordercolor=0x16141C@0.65:x=(w-text_w)*%.3f:y=(h-text_h)*%.3f:enable='between(t\\,%.3f\\,%.3f)'[%s];",
				vCur, escapeFilter(font), escapeFilter(text.TextFile), text.X, text.Y, text.StartSec, text.EndSec, next)
			vCur = next
		}
	}
	fmt.Fprintf(&b, "[%s]format=yuv420p[vout];", vCur)

	if spec.Music != nil && musicAudio {
		idx := len(spec.Clips)
		fmt.Fprintf(&b, "[%d:a]aformat=sample_fmts=fltp:sample_rates=44100:channel_layouts=stereo,atrim=start=%.3f,asetpts=PTS-STARTPTS,volume=%.3f",
			idx, spec.Music.InSec, spec.Music.Volume)
		if spec.Music.OffsetSec > 0.01 {
			ms := int(spec.Music.OffsetSec * 1000)
			fmt.Fprintf(&b, ",adelay=%d|%d", ms, ms)
		}
		fmt.Fprintf(&b, ",apad,atrim=0:%.3f[music];", elapsed)
		fmt.Fprintf(&b, "[%s][music]amix=inputs=2:duration=first:normalize=0[aout]", aCur)
	} else {
		fmt.Fprintf(&b, "[%s]anull[aout]", aCur)
	}
	return b.String(), nil
}

func escapeFilter(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.ReplaceAll(path, ":", "\\:")
	path = strings.ReplaceAll(path, "'", "\\'")
	return path
}
