package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/download-service/internal/domain"
	"github.com/tapenest/tapenest/services/download-service/internal/mq"
	"github.com/tapenest/tapenest/services/download-service/internal/netguard"
	"github.com/tapenest/tapenest/services/download-service/internal/repo"
	"github.com/tapenest/tapenest/services/download-service/internal/ytdlp"
)

// mobileUA is used on the last escalation step (spec §5.3: mobile + other UA).
const mobileUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Mobile/15E148 Safari/604.1"

// ProcessorConfig are worker limits.
type ProcessorConfig struct {
	Limits      domain.Limits
	MaxFilesize int64
	MaxDuration time.Duration
	JobTimeout  time.Duration
	Retention   time.Duration
	PresignTTL  time.Duration
	TmpDir      string
	// delays for "come back later" situations (not counted as attempts)
	LockedDelay time.Duration // same URL is being downloaded by another worker
	BusyDelay   time.Duration // per-domain semaphore is full
}

// Processor runs one job end to end.
type Processor struct {
	mediaIndex
	cfg     ProcessorConfig
	queue   Queue
	fetch   Fetcher
	limiter Limiter
	locker  Locker
	plans   Planner
	guard   HostGuard
	cookies CookieJar
	proxies ProxyPicker
	editor  Editor
}

// ProcessorDeps groups dependencies.
type ProcessorDeps struct {
	Config  ProcessorConfig
	Store   Store
	Queue   Queue
	Bus     Bus
	Files   Files
	Fetch   Fetcher
	Limiter Limiter
	Locker  Locker
	Plans   Planner
	Guard   HostGuard
	Cookies CookieJar
	Proxies ProxyPicker
	Editor  Editor
	Log     *slog.Logger
	Now     func() time.Time
}

// NewProcessor builds a Processor.
func NewProcessor(d ProcessorDeps) *Processor {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Config.LockedDelay == 0 {
		d.Config.LockedDelay = 5 * time.Second
	}
	if d.Config.BusyDelay == 0 {
		d.Config.BusyDelay = 3 * time.Second
	}
	return &Processor{
		mediaIndex: mediaIndex{store: d.Store, bus: d.Bus, files: d.Files, log: d.Log, now: d.Now},
		cfg:        d.Config, queue: d.Queue, fetch: d.Fetch, limiter: d.Limiter, locker: d.Locker,
		plans: d.Plans, guard: d.Guard, cookies: d.Cookies, proxies: d.Proxies, editor: d.Editor,
	}
}

// failure is a classified download error.
type failure struct {
	kind domain.ErrorKind
	msg  string
}

func (f *failure) Error() string { return string(f.kind) + ": " + f.msg }

func fail(kind domain.ErrorKind, msg string) *failure { return &failure{kind: kind, msg: msg} }

// Process handles one queued job. A returned error means infrastructure trouble:
// the queue entry is not acked and will be reclaimed; job outcomes (done, failed,
// retry scheduled) return nil.
func (p *Processor) Process(ctx context.Context, id uuid.UUID) error {
	job, err := p.store.GetJob(ctx, id)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if job.Status.Terminal() {
		return nil
	}
	log := p.log.With("job", job.ID, "source", job.Source)

	unlock, ok, err := p.locker.Lock(ctx, "download:lock:"+job.URLHash, p.cfg.JobTimeout+time.Minute)
	if err != nil {
		return err
	}
	if !ok { // another worker downloads the same video; come back and reuse its result
		return p.queue.EnqueueAt(ctx, job.ID, job.Priority, p.now().Add(p.cfg.LockedDelay))
	}
	defer unlock()

	if m, hit, err := p.lookup(ctx, job.URLHash); err != nil {
		return err
	} else if hit {
		log.InfoContext(ctx, "dedup hit under lock")
		return p.finish(ctx, job, m, true)
	}

	holder := job.ID.String()
	if ok, err := p.limiter.Acquire(ctx, job.Source, holder); err != nil {
		return err
	} else if !ok {
		return p.queue.EnqueueAt(ctx, job.ID, job.Priority, p.now().Add(p.cfg.BusyDelay))
	}
	defer func() { _ = p.limiter.Release(context.WithoutCancel(ctx), job.Source, holder) }()

	job, err = p.store.MarkRunning(ctx, job.ID, domain.StageProbing)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	p.progress(ctx, job, domain.Progress{Stage: domain.StageProbing}, true)

	plan, err := p.plans.Get(ctx, job.ID)
	if err != nil {
		return err
	}
	if plan.Tier == "" {
		plan.Tier = domain.TierDatacenter
	}
	avoid := ""
	if plan.NewProxy {
		avoid = plan.LastProxy
	}
	proxyURL, tier := p.proxies.Pick(plan.Tier, avoid)

	jctx, cancel := context.WithTimeout(ctx, p.cfg.JobTimeout)
	defer cancel()
	dir, err := os.MkdirTemp(p.cfg.TmpDir, "dl-"+job.ID.String()+"-")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	opts := ytdlp.Opts{Proxy: proxyURL, MaxFilesize: p.cfg.MaxFilesize}
	if plan.WithCookies {
		if jar, ok, err := p.cookies.WriteFile(jctx, job.Source, dir); err != nil {
			log.WarnContext(ctx, "cookies unavailable", "err", err)
		} else if ok {
			opts.CookiesFile = jar.Path
			if jar.Stale {
				log.WarnContext(ctx, "cookies are stale", "age", jar.Age.String()) // alert (spec: > 12 h)
			}
		}
	}
	if plan.MobileUA {
		opts.UserAgent = mobileUA
	}
	log.InfoContext(ctx, "download attempt", "attempt", job.Attempts, "tier", tier, "cookies", opts.CookiesFile != "")

	m, derr := p.download(jctx, job, opts, dir)
	if derr == nil {
		_ = p.plans.Delete(ctx, job.ID)
		return p.finish(ctx, job, m, false)
	}
	if ctx.Err() != nil { // shutdown: put the job back untouched
		_, _ = p.store.Requeue(context.WithoutCancel(ctx), job.ID, job.Priority, "", "")
		return p.queue.Enqueue(context.WithoutCancel(ctx), job.ID, job.Priority)
	}
	var f *failure
	if !errors.As(derr, &f) {
		f = fail(domain.KindInternal, derr.Error())
		if errors.Is(derr, context.DeadlineExceeded) {
			f = fail(domain.KindNetwork, "job timeout")
		}
	}
	log.WarnContext(ctx, "download failed", "kind", f.kind, "msg", f.msg, "attempt", job.Attempts)
	return p.retryOrFail(ctx, job, f, proxyURL)
}

func (p *Processor) retryOrFail(ctx context.Context, job domain.Job, f *failure, usedProxy string) error {
	st := domain.Plan(f.kind, job.Attempts)
	if st.Retry {
		if err := p.plans.Set(ctx, job.ID, mq.Plan{Attempt: st.Next, LastProxy: usedProxy}); err != nil {
			return err
		}
		j, err := p.store.Requeue(ctx, job.ID, domain.PriorityLow, f.kind, f.msg)
		if err != nil {
			return err
		}
		e := eventFor(mq.EventProgress, j)
		e.ErrorKind = f.kind
		_ = p.bus.Publish(ctx, e)
		return p.queue.EnqueueAt(ctx, job.ID, domain.PriorityLow, p.now().Add(st.Next.Delay))
	}
	_ = p.plans.Delete(ctx, job.ID)
	j, err := p.store.FinishFailed(ctx, job.ID, f.kind, f.msg)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	e := eventFor(mq.EventFailed, j)
	e.ErrorKind = f.kind
	return p.bus.Publish(ctx, e)
}

func (p *Processor) finish(ctx context.Context, job domain.Job, m domain.Media, cached bool) error {
	j, err := p.store.FinishDone(ctx, job.ID, m.ID, m.Title)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	p.log.InfoContext(ctx, "download done", "job", j.ID, "source", j.Source, "bytes", m.SizeBytes, "height", m.Height, "cached", cached)
	e := eventFor(mq.EventDownloaded, j)
	e.File, e.Cached = p.fileInfo(ctx, m, p.cfg.PresignTTL), cached
	return p.bus.Publish(ctx, e)
}

// download = SSRF check → pass 1 (metadata) → checks → format → pass 2 → S3.
func (p *Processor) download(ctx context.Context, job domain.Job, opts ytdlp.Opts, dir string) (domain.Media, error) {
	u, err := url.Parse(job.Normalized)
	if err != nil {
		return domain.Media{}, fail(domain.KindUnsupported, "bad url")
	}
	if err := p.guard.Check(ctx, u.Hostname()); errors.Is(err, netguard.ErrForbidden) {
		return domain.Media{}, fail(domain.KindUnsupported, "host resolves to a private address")
	} else if err != nil {
		return domain.Media{}, fail(domain.KindNetwork, err.Error())
	}
	infoPath := filepath.Join(dir, "info.json")
	info, err := p.fetch.Probe(ctx, job.Normalized, opts, infoPath)
	if err != nil {
		return domain.Media{}, ytErr(err)
	}
	switch {
	case info.Live():
		return domain.Media{}, fail(domain.KindLive, "live stream or premiere")
	case info.DRM():
		return domain.Media{}, fail(domain.KindDRM, "DRM-protected media is never downloaded")
	case p.cfg.MaxDuration > 0 && info.Duration > p.cfg.MaxDuration.Seconds():
		return domain.Media{}, fail(domain.KindTooLarge, fmt.Sprintf("duration %.0fs exceeds the limit", info.Duration))
	}
	choice, ok := domain.SelectFormat(info, p.cfg.Limits)
	if !ok {
		return domain.Media{}, fail(domain.KindUnavailable, "no suitable format")
	}
	if choice.EstSize > p.cfg.MaxFilesize {
		return domain.Media{}, fail(domain.KindTooLarge, fmt.Sprintf("estimated size %d exceeds the limit", choice.EstSize))
	}
	title := strings.TrimSpace(info.Title)
	job.Title = title
	if err := p.store.SetStage(ctx, job.ID, domain.StageDownloading, title); err != nil {
		return domain.Media{}, err
	}
	onProgress := p.progressFn(ctx, job, choice.EstSize)
	onProgress(ytdlp.Progress{})

	mediaID := uuid.New()
	ext := choice.Ext
	if ext == "" || !choice.Single {
		ext = "mp4"
	}
	mime := "video/" + ext
	name := FileName(title, info.ID, mime)
	key := fmt.Sprintf("%s/%s/%s.%s", job.Source, p.now().UTC().Format("2006/01"), mediaID, ext)

	var size int64
	posterKey := ""
	if choice.Single { // one progressive file: yt-dlp stdout → S3, no temp file
		rc, wait, err := p.fetch.Stream(ctx, infoPath, choice.Spec, opts, onProgress)
		if err != nil {
			return domain.Media{}, err
		}
		n, perr := p.files.Put(ctx, key, rc, -1, mime, name)
		_ = rc.Close()
		werr := wait()   // always reap yt-dlp
		if perr != nil { // storage failed first (yt-dlp then dies on the closed pipe)
			return domain.Media{}, fail(domain.KindNetwork, "storage: "+perr.Error())
		}
		if werr != nil {
			return domain.Media{}, ytErr(werr)
		}
		size = n
	} else { // merge (video + audio) needs a seekable temp file
		path, err := p.fetch.DownloadFile(ctx, infoPath, choice.Spec, dir, opts, onProgress)
		if err != nil {
			return domain.Media{}, ytErr(err)
		}
		f, err := os.Open(path) //nolint:gosec // path is inside our temp dir (checked by the runner)
		if err != nil {
			return domain.Media{}, fmt.Errorf("open output: %w", err)
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return domain.Media{}, fmt.Errorf("stat output: %w", err)
		}
		if st.Size() > p.cfg.MaxFilesize {
			return domain.Media{}, fail(domain.KindTooLarge, "file exceeds the limit")
		}
		if p.editor != nil {
			frame := filepath.Join(dir, "frame.jpg")
			if err := p.editor.Poster(ctx, path, frame); err != nil {
				p.log.WarnContext(ctx, "poster extract failed", "err", err)
			} else if pf, err := os.Open(frame); err != nil { //nolint:gosec // path is inside our temp dir
				p.log.WarnContext(ctx, "poster open failed", "err", err)
			} else {
				pkey := "posters/" + mediaID.String() + ".jpg"
				if _, perr := p.files.Put(ctx, pkey, pf, -1, "image/jpeg", "poster.jpg"); perr != nil {
					p.log.WarnContext(ctx, "poster upload failed", "err", perr)
				} else {
					posterKey = pkey
				}
				_ = pf.Close()
			}
		}
		_ = p.store.SetStage(ctx, job.ID, domain.StageUploading, "")
		p.progress(ctx, job, domain.Progress{Stage: domain.StageUploading, Pct: 99, DownloadedBytes: st.Size(), TotalBytes: st.Size()}, true)
		if size, err = p.files.Put(ctx, key, f, st.Size(), mime, name); err != nil {
			return domain.Media{}, fail(domain.KindNetwork, err.Error())
		}
	}
	m, err := p.store.UpsertMedia(ctx, domain.Media{
		ID: mediaID, URLHash: job.URLHash, URL: job.Normalized, Source: job.Source, ExternalID: info.ID,
		Title: title, DurationSec: int(math.Round(info.Duration)), Width: widthFor(info, choice), Height: choice.Height,
		FormatID: choice.Spec, ObjectKey: key, SizeBytes: size, MimeType: mime, Thumbnail: info.Thumbnail,
		PosterKey: posterKey, ExpiresAt: p.now().Add(p.cfg.Retention),
	})
	if err != nil {
		return domain.Media{}, err
	}
	if err := p.bus.CacheMedia(ctx, job.URLHash, m.ID); err != nil {
		p.log.WarnContext(ctx, "dedup cache write failed", "err", err)
	}
	return m, nil
}

func widthFor(info domain.Info, c domain.Choice) int {
	vid, _, _ := strings.Cut(c.Spec, "+")
	for _, f := range info.Formats {
		if f.ID == vid {
			return f.Width
		}
	}
	return 0
}

func ytErr(err error) error {
	var ye *ytdlp.Error
	if errors.As(err, &ye) {
		return fail(ye.Kind, ye.Msg)
	}
	return err
}

// progressFn converts yt-dlp progress to job progress; Redis/SSE at most every
// 500 ms, bot events only on 25 % steps (Telegram edit limits).
func (p *Processor) progressFn(ctx context.Context, job domain.Job, est int64) func(ytdlp.Progress) {
	var mu sync.Mutex
	var last time.Time
	lastStep := -1
	return func(yp ytdlp.Progress) {
		total := max(yp.TotalBytes, est) // a merge reports the video part first

		pct := 0.0
		if total > 0 {
			pct = math.Min(99, float64(yp.DownloadedBytes)*100/float64(total))
		}
		pr := domain.Progress{Stage: domain.StageDownloading, Pct: math.Round(pct*10) / 10, DownloadedBytes: yp.DownloadedBytes, TotalBytes: total, SpeedBps: yp.SpeedBps, EtaSec: yp.EtaSec}
		mu.Lock()
		defer mu.Unlock()
		step := int(pct) / 25
		now := p.now()
		if now.Sub(last) < 500*time.Millisecond && step == lastStep {
			return
		}
		last = now
		toBot := step != lastStep
		lastStep = step
		p.progress(ctx, job, pr, toBot)
	}
}

func (p *Processor) progress(ctx context.Context, job domain.Job, pr domain.Progress, toBot bool) {
	if err := p.bus.SetProgress(ctx, job.ID, pr); err != nil {
		p.log.WarnContext(ctx, "progress write failed", "err", err)
	}
	if toBot && job.Chat != nil {
		e := eventFor(mq.EventProgress, job)
		e.Status, e.Progress = domain.StatusRunning, &pr
		_ = p.bus.Publish(ctx, e)
	}
}
