package service

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/ffmpeg"
)

// Join math matches apps/waveplayer/src/features/videos/editor/model.ts.
const (
	minSourceSec   = 0.5
	minOutputSec   = 0.5
	maxClips       = 8
	maxTexts       = 8
	maxTimelineSec = 600
	joinFraction   = 0.45
	canvasWidth    = 1280
	canvasHeight   = 720
)

// Project is a timeline export (POST /api/v1/downloads/compose).
type Project struct {
	Title string        `json:"title"`
	Clips []ProjectClip `json:"clips"`
	Texts []ProjectText `json:"texts"`
	Music *ProjectMusic `json:"music"`
}

// ProjectClip is one segment of a download the caller already owns.
type ProjectClip struct {
	JobID         string      `json:"jobId"`
	InSec         float64     `json:"inSec"`
	OutSec        float64     `json:"outSec"`
	Speed         float64     `json:"speed"`
	Volume        float64     `json:"volume"`
	Crop          ProjectCrop `json:"crop"`
	Rotate        int         `json:"rotate"`
	Transition    string      `json:"transition"`
	TransitionSec float64     `json:"transitionSec"`
}

// ProjectCrop is a normalized window (0..1) inside the source frame.
type ProjectCrop struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

// ProjectText is an overlay with its own in/out on the exported timeline.
type ProjectText struct {
	Text     string  `json:"text"`
	StartSec float64 `json:"startSec"`
	EndSec   float64 `json:"endSec"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
}

// ProjectMusic is audio taken from another of the caller's downloads.
type ProjectMusic struct {
	JobID     string  `json:"jobId"`
	InSec     float64 `json:"inSec"`
	Volume    float64 `json:"volume"`
	OffsetSec float64 `json:"offsetSec"`
}

type plannedClip struct {
	jobID uuid.UUID
	ffmpeg.ComposeClip
	join float64
}

type plan struct {
	title    string
	clips    []plannedClip
	texts    []ProjectText
	music    *ProjectMusic
	musicID  uuid.UUID
	duration float64
}

// Compose renders a timeline of this user's finished downloads into a new library item.
func (a *API) Compose(ctx context.Context, userID uuid.UUID, raw Project) (View, error) {
	if a.editor == nil {
		return View{}, ErrEditUnavailable
	}
	planned, err := normalizeProject(raw)
	if err != nil {
		return View{}, err
	}
	type loaded struct {
		job   domain.Job
		media domain.Media
	}
	cache := map[uuid.UUID]loaded{}
	load := func(id uuid.UUID) (loaded, error) {
		if hit, ok := cache[id]; ok {
			return hit, nil
		}
		j, m, err := a.ownedDone(ctx, userID, id)
		if err != nil {
			return loaded{}, err
		}
		hit := loaded{job: j, media: m}
		cache[id] = hit
		return hit, nil
	}
	for i := range planned.clips {
		hit, err := load(planned.clips[i].jobID)
		if err != nil {
			return View{}, err
		}
		if hit.media.DurationSec <= 0 || planned.clips[i].OutSec > float64(hit.media.DurationSec)+0.05 {
			return View{}, ErrBadProject
		}
	}
	var musicHit loaded
	if planned.music != nil {
		musicHit, err = load(planned.musicID)
		if err != nil {
			return View{}, err
		}
		if musicHit.media.DurationSec <= 0 || planned.music.InSec >= float64(musicHit.media.DurationSec) {
			return View{}, ErrBadProject
		}
	}
	dir, err := os.MkdirTemp("", "tn-compose-")
	if err != nil {
		return View{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	local := map[string]string{}
	pathFor := func(key string) (string, error) {
		if p, ok := local[key]; ok {
			return p, nil
		}
		p := filepath.Join(dir, fmt.Sprintf("src-%d%s", len(local), extOf(key)))
		if err := a.copyObject(ctx, key, p); err != nil {
			return "", err
		}
		local[key] = p
		return p, nil
	}
	spec := ffmpeg.ComposeSpec{Texts: make([]ffmpeg.ComposeText, len(planned.texts))}
	for _, clip := range planned.clips {
		hit := cache[clip.jobID]
		path, err := pathFor(hit.media.ObjectKey)
		if err != nil {
			return View{}, fmt.Errorf("read source: %w", err)
		}
		clip.Path = path
		spec.Clips = append(spec.Clips, clip.ComposeClip)
	}
	for i, text := range planned.texts {
		spec.Texts[i] = ffmpeg.ComposeText{Text: text.Text, StartSec: text.StartSec, EndSec: text.EndSec, X: text.X, Y: text.Y}
	}
	if planned.music != nil {
		path, err := pathFor(musicHit.media.ObjectKey)
		if err != nil {
			return View{}, fmt.Errorf("read music: %w", err)
		}
		spec.Music = &ffmpeg.ComposeMusic{Path: path, InSec: planned.music.InSec, Volume: planned.music.Volume, OffsetSec: planned.music.OffsetSec}
	}
	out := filepath.Join(dir, "out.mp4")
	if err := a.editor.Compose(ctx, spec, out); err != nil {
		a.log.WarnContext(ctx, "compose failed", "err", err)
		return View{}, ErrEditFailed
	}
	first := cache[planned.clips[0].jobID]
	dur := int(math.Round(planned.duration))
	if dur < 1 {
		dur = 1
	}
	return a.persistRendered(ctx, userID, renderedFile{
		localPath: out, title: planned.title, duration: dur, formatID: "compose",
		width: canvasWidth, height: canvasHeight, sourceJob: first.job, sourceMedia: first.media,
	})
}

func (a *API) ownedDone(ctx context.Context, userID, id uuid.UUID) (domain.Job, domain.Media, error) {
	j, err := a.store.GetUserJob(ctx, userID, id)
	if err != nil {
		return domain.Job{}, domain.Media{}, err
	}
	if j.Status != domain.StatusDone || j.MediaID == nil {
		return domain.Job{}, domain.Media{}, ErrNotReady
	}
	m, err := a.store.GetMedia(ctx, *j.MediaID)
	if err != nil {
		return domain.Job{}, domain.Media{}, err
	}
	return j, m, nil
}

func normalizeProject(raw Project) (plan, error) {
	title, err := cleanTitle(raw.Title)
	if err != nil {
		return plan{}, err
	}
	if len(raw.Clips) < 1 || len(raw.Clips) > maxClips || len(raw.Texts) > maxTexts {
		return plan{}, ErrBadProject
	}
	out := plan{title: title, texts: raw.Texts}
	var total float64
	for i, clip := range raw.Clips {
		id, err := uuid.Parse(clip.JobID)
		if err != nil {
			return plan{}, ErrBadProject
		}
		speed, ok := allowedSpeed(clip.Speed)
		if !ok || !finite(clip.InSec, clip.OutSec, clip.Volume, clip.TransitionSec, clip.Crop.X, clip.Crop.Y, clip.Crop.W, clip.Crop.H) {
			return plan{}, ErrBadProject
		}
		if clip.InSec < 0 || clip.OutSec-clip.InSec < minSourceSec-1e-6 {
			return plan{}, ErrBadProject
		}
		if (clip.OutSec-clip.InSec)/speed < minOutputSec-1e-6 {
			return plan{}, ErrBadProject
		}
		if clip.Volume < 0 || clip.Volume > 1.5 {
			return plan{}, ErrBadProject
		}
		if clip.Rotate != 0 && clip.Rotate != 90 && clip.Rotate != 180 && clip.Rotate != 270 {
			return plan{}, ErrBadProject
		}
		if clip.Crop.X < -0.001 || clip.Crop.Y < -0.001 || clip.Crop.W < 0.1 || clip.Crop.H < 0.1 ||
			clip.Crop.X+clip.Crop.W > 1.001 || clip.Crop.Y+clip.Crop.H > 1.001 {
			return plan{}, ErrBadProject
		}
		transition := clip.Transition
		if i == 0 {
			transition = "none"
		}
		switch transition {
		case "", "none", "fade", "wipe":
		default:
			return plan{}, ErrBadProject
		}
		item := plannedClip{
			jobID: id,
			ComposeClip: ffmpeg.ComposeClip{
				InSec: clip.InSec, OutSec: clip.OutSec, Speed: speed, Volume: clip.Volume,
				CropX: clip.Crop.X, CropY: clip.Crop.Y, CropW: clip.Crop.W, CropH: clip.Crop.H,
				Rotate: clip.Rotate, Transition: transition, TransitionSec: clip.TransitionSec,
			},
		}
		if i > 0 {
			item.join = joinOf(out.clips[i-1].ComposeClip, item.ComposeClip)
			item.TransitionSec = item.join
		}
		total += (item.OutSec - item.InSec) / item.Speed
		if i > 0 {
			total -= item.join
		}
		out.clips = append(out.clips, item)
	}
	if total <= 0 || total > maxTimelineSec {
		return plan{}, ErrBadProject
	}
	out.duration = total
	for i, text := range raw.Texts {
		clean, ok := cleanOverlay(text.Text)
		if !ok || !finite(text.StartSec, text.EndSec, text.X, text.Y) {
			return plan{}, ErrBadProject
		}
		if text.StartSec < 0 || text.EndSec-text.StartSec < 0.2 || text.EndSec > total+0.05 || text.X < 0 || text.X > 1 || text.Y < 0 || text.Y > 1 {
			return plan{}, ErrBadProject
		}
		out.texts[i].Text = clean
	}
	if raw.Music != nil {
		id, err := uuid.Parse(raw.Music.JobID)
		if err != nil || !finite(raw.Music.InSec, raw.Music.Volume, raw.Music.OffsetSec) {
			return plan{}, ErrBadProject
		}
		if raw.Music.InSec < 0 || raw.Music.Volume < 0 || raw.Music.Volume > 1.5 || raw.Music.OffsetSec < 0 || raw.Music.OffsetSec >= total {
			return plan{}, ErrBadProject
		}
		out.music = raw.Music
		out.musicID = id
	}
	return out, nil
}

func allowedSpeed(v float64) (float64, bool) {
	for _, speed := range []float64{0.5, 1, 1.5, 2} {
		if math.Abs(v-speed) < 0.001 {
			return speed, true
		}
	}
	return 0, false
}

func finite(vals ...float64) bool {
	for _, v := range vals {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

func joinOf(prev, cur ffmpeg.ComposeClip) float64 {
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
	cap := (prev.OutSec - prev.InSec) / prev.Speed
	if other := (cur.OutSec - cur.InSec) / cur.Speed; other < cap {
		cap = other
	}
	cap *= joinFraction
	if td > cap {
		td = cap
	}
	if td < 0.05 {
		return 0
	}
	return math.Round(td*1000) / 1000
}

func cleanOverlay(raw string) (string, bool) {
	s := squashText(raw)
	n := utf8.RuneCountInString(s)
	if n < 1 || n > 80 {
		return "", false
	}
	return s, true
}
