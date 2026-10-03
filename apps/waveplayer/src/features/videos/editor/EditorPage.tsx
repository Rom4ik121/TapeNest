import { Pause, Play, Redo2, Scissors, Undo2 } from 'lucide-react';
import { useEffect, useReducer, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate, useParams } from 'react-router-dom';
import { useQueries } from '@tanstack/react-query';
import { downloadErrorKey, downloadsApi } from '@/entities/video/api';
import { useComposeVideo, useVideo, useVideoList } from '@/entities/video/queries';
import { parseClock } from '@/entities/video/range';
import { qk } from '@/shared/api/queryClient';
import { formatTime } from '@/shared/lib/format';
import { haptic } from '@/shared/telegram';
import { ErrorState } from '@/shared/ui/States';
import type { VideoJob } from '@/entities/video/types';
import { PreviewStage } from './PreviewStage';
import { Timeline } from './Timeline';
import {
  SPEEDS,
  addClip,
  addText,
  clearMusic,
  clipFromJob,
  commit,
  cropFromFrame,
  emptyHistory,
  frameFromCrop,
  moveClip,
  nextRotate,
  patchClip,
  patchText,
  placeAt,
  projectDuration,
  redo,
  removeText,
  setMusic,
  splitClip,
  toCompose,
  trimClip,
  uid,
  undo,
  type History,
  type Project,
  type TransitionKind,
} from './model';

type HistoryAction =
  | { type: 'reset'; history: History }
  | { type: 'commit'; project: Project }
  | { type: 'preview'; project: Project }
  | { type: 'commitFrom'; base: Project; project: Project }
  | { type: 'undo' }
  | { type: 'redo' };

function historyReducer(state: History | null, action: HistoryAction): History | null {
  switch (action.type) {
    case 'reset':
      return action.history;
    case 'commit':
      return state ? commit(state, action.project) : state;
    case 'preview':
      return state ? { ...state, present: action.project } : state;
    case 'commitFrom':
      return commit({ past: state?.past ?? [], present: action.base, future: [] }, action.project);
    case 'undo':
      return state ? undo(state) : state;
    case 'redo':
      return state ? redo(state) : state;
    default: {
      const never: never = action;
      return never;
    }
  }
}

const TRANSITIONS: readonly TransitionKind[] = ['none', 'fade', 'wipe'];

export function EditorPage() {
  const { id = '' } = useParams();
  const { t } = useTranslation();
  const navigate = useNavigate();
  const video = useVideo(id);
  const list = useVideoList();
  const compose = useComposeVideo();
  const job = video.data;
  const [history, dispatch] = useReducer(historyReducer, null);
  const [selectedId, setSelectedId] = useState('');
  const [time, setTime] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [exportName, setExportName] = useState('');
  const [hint, setHint] = useState<string | null>(null);
  const [picker, setPicker] = useState<'clip' | 'music' | null>(null);
  const seeded = useRef(false);
  const dragBase = useRef<Project | null>(null);
  const present = history?.present ?? null;

  useEffect(() => {
    if (!job || job.status !== 'done' || seeded.current) return;
    const clip = clipFromJob(job, uid());
    if (!clip) return;
    seeded.current = true;
    dispatch({ type: 'reset', history: emptyHistory({ clips: [clip], texts: [], music: null }) });
    setSelectedId(clip.id);
    setExportName(t('videos.editor.exportName', { title: job.title?.trim() || t('videos.untitled') }));
  }, [job, t]);

  const sourceIds = present
    ? [...new Set([...present.clips.map((clip) => clip.jobId), ...(present.music ? [present.music.jobId] : [])])]
    : job
      ? [job.id]
      : [];
  const files = useQueries({
    queries: sourceIds.map((jobId) => ({
      queryKey: qk.videos.file(jobId),
      queryFn: ({ signal }: { signal: AbortSignal }) => downloadsApi.fileUrl(jobId, signal),
      staleTime: 30 * 60 * 1000,
    })),
  });
  const urls: Record<string, string | undefined> = {};
  sourceIds.forEach((jobId, index) => {
    urls[jobId] = files[index]?.data?.url;
  });

  const duration = present ? projectDuration(present) : 0;
  useEffect(() => {
    if (!playing || !present) return;
    let last = performance.now();
    let frame = 0;
    const tick = (now: number) => {
      const step = (now - last) / 1000;
      last = now;
      setTime((current) => {
        const next = current + step;
        if (next >= duration - 0.02) {
          setPlaying(false);
          return duration;
        }
        return next;
      });
      frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [duration, playing, present]);

  if (video.isPending || (job?.status === 'done' && !present)) {
    return <div className="h-48 animate-pulse rounded-3xl bg-surface-2" aria-busy="true" />;
  }
  if (video.isError || !job) {
    return <ErrorState error={video.error} onRetry={() => void video.refetch()} title={t('videos.notFound')} />;
  }
  if (job.status !== 'done' || !present) {
    return <p className="px-1 text-sm text-muted">{t('videos.editor.notReady')}</p>;
  }

  const selected = present.clips.find((clip) => clip.id === selectedId) ?? present.clips[0];
  if (!selected) return null;
  const frame = frameFromCrop(selected.crop);
  const doneJobs = (list.data?.items ?? []).filter(
    (item) => item.status === 'done' && (item.file?.durationSec ?? 0) >= 0.5,
  );

  const apply = (next: Project | null, note?: string) => {
    if (!next) {
      setHint(note ?? t('videos.editor.tooShort'));
      return;
    }
    setHint(null);
    dispatch({ type: 'commit', project: next });
  };

  const split = () => {
    const place = placeAt(present, time);
    const clip = place ? present.clips[place.clip.index] : undefined;
    if (!clip || !place) return;
    const rightId = uid();
    const next = splitClip(present, clip.id, place.clip.sourceSec, rightId);
    if (!next) {
      setHint(t('videos.editor.noSplit'));
      return;
    }
    setHint(null);
    haptic('light');
    dispatch({ type: 'commit', project: next.project });
    setSelectedId(next.rightId);
  };

  const exportVideo = () => {
    setHint(null);
    haptic('medium');
    compose.mutate(toCompose(present, exportName), {
      onSuccess: (created) => navigate(`/videos/${created.id}`, { replace: true }),
      onError: (error) => setHint(t(downloadErrorKey(error))),
    });
  };

  const transitionLabel = (kind: TransitionKind): string => {
    switch (kind) {
      case 'none':
        return t('videos.editor.transitionNone');
      case 'fade':
        return t('videos.editor.transitionFade');
      case 'wipe':
        return t('videos.editor.transitionWipe');
      default: {
        const never: never = kind;
        return never;
      }
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex items-end justify-between gap-3 px-1">
        <h1 className="text-xl font-extrabold leading-tight">{t('videos.editor.title')}</h1>
        <p className="text-xs tabular-nums text-muted">
          {formatTime(time)} / {formatTime(duration)}
        </p>
      </div>

      <PreviewStage project={present} time={time} playing={playing} urls={urls} />

      <div className="flex items-center gap-2">
        <button
          type="button"
          aria-label={playing ? t('videos.editor.pause') : t('videos.editor.play')}
          onClick={() => {
            haptic('light');
            if (time >= duration - 0.05) setTime(0);
            setPlaying((value) => !value);
          }}
          className="grid h-11 w-11 place-items-center rounded-full bg-accent text-accent-fg"
        >
          {playing ? <Pause className="h-5 w-5" aria-hidden /> : <Play className="h-5 w-5" aria-hidden />}
        </button>
        <button
          type="button"
          aria-label={t('videos.editor.undo')}
          disabled={!history || history.past.length === 0}
          onClick={() => dispatch({ type: 'undo' })}
          className="grid h-11 w-11 place-items-center rounded-2xl border border-line disabled:opacity-40"
        >
          <Undo2 className="h-4 w-4" aria-hidden />
        </button>
        <button
          type="button"
          aria-label={t('videos.editor.redo')}
          disabled={!history || history.future.length === 0}
          onClick={() => dispatch({ type: 'redo' })}
          className="grid h-11 w-11 place-items-center rounded-2xl border border-line disabled:opacity-40"
        >
          <Redo2 className="h-4 w-4" aria-hidden />
        </button>
        <button
          type="button"
          onClick={split}
          className="inline-flex h-11 flex-1 items-center justify-center gap-2 rounded-2xl bg-plum text-sm font-semibold text-cream"
        >
          <Scissors className="h-4 w-4" aria-hidden />
          {t('videos.editor.split')}
        </button>
      </div>

      <Timeline
        project={present}
        time={time}
        selectedId={selected.id}
        onSelect={(clipId) => {
          setSelectedId(clipId);
          setPlaying(false);
        }}
        onSeek={(next) => {
          setPlaying(false);
          setTime(next);
        }}
        onPreview={(next) => {
          if (!dragBase.current) dragBase.current = present;
          dispatch({ type: 'preview', project: next });
        }}
        onCommit={(next) => {
          const base = dragBase.current;
          dragBase.current = null;
          if (base) dispatch({ type: 'commitFrom', base, project: next });
          else dispatch({ type: 'commit', project: next });
        }}
      />

      <section className="space-y-3 rounded-3xl bg-surface p-4 shadow-card">
        <div className="grid grid-cols-2 gap-2">
          <label className="text-xs font-medium text-muted">
            {t('videos.editor.in')}
            <input
              value={formatTime(selected.inSec)}
              onChange={(event) => {
                const sec = parseClock(event.target.value);
                if (sec === null) return;
                apply(trimClip(present, selected.id, 'in', sec));
              }}
              inputMode="numeric"
              className="mt-1 h-11 w-full rounded-xl border border-line bg-bg px-3 text-sm text-fg outline-none ring-accent focus:ring-2"
            />
          </label>
          <label className="text-xs font-medium text-muted">
            {t('videos.editor.out')}
            <input
              value={formatTime(selected.outSec)}
              onChange={(event) => {
                const sec = parseClock(event.target.value);
                if (sec === null) return;
                apply(trimClip(present, selected.id, 'out', sec));
              }}
              inputMode="numeric"
              className="mt-1 h-11 w-full rounded-xl border border-line bg-bg px-3 text-sm text-fg outline-none ring-accent focus:ring-2"
            />
          </label>
        </div>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => apply(moveClip(present, selected.id, -1))}
            className="h-10 flex-1 rounded-xl border border-line text-sm font-semibold"
          >
            {t('videos.editor.earlier')}
          </button>
          <button
            type="button"
            onClick={() => apply(moveClip(present, selected.id, 1))}
            className="h-10 flex-1 rounded-xl border border-line text-sm font-semibold"
          >
            {t('videos.editor.later')}
          </button>
        </div>

        <div>
          <p className="text-xs font-medium text-muted">{t('videos.editor.speed')}</p>
          <div className="mt-1 grid grid-cols-4 gap-1">
            {SPEEDS.map((speed) => (
              <button
                key={speed}
                type="button"
                aria-label={t('videos.editor.speedValue', { value: speed })}
                aria-pressed={selected.speed === speed}
                onClick={() => apply(patchClip(present, selected.id, { speed }))}
                className={`h-10 rounded-xl text-sm font-semibold ${selected.speed === speed ? 'bg-accent text-accent-fg' : 'border border-line'}`}
              >
                {speed}×
              </button>
            ))}
          </div>
        </div>

        <label className="block text-xs font-medium text-muted">
          {t('videos.editor.volume')}
          <input
            type="range"
            aria-label={t('videos.editor.volume')}
            min={0}
            max={1.5}
            step={0.05}
            value={selected.volume}
            onChange={(event) => apply(patchClip(present, selected.id, { volume: Number(event.target.value) }))}
            className="mt-1 h-2 w-full accent-amber"
          />
        </label>

        <div className="grid grid-cols-2 gap-2">
          <label className="text-xs font-medium text-muted">
            {t('videos.editor.zoom')}
            <input
              type="range"
              aria-label={t('videos.editor.zoom')}
              min={1}
              max={3}
              step={0.1}
              value={frame.zoom}
              onChange={(event) =>
                apply(
                  patchClip(present, selected.id, {
                    crop: cropFromFrame(Number(event.target.value), frame.panX, frame.panY),
                  }),
                )
              }
              className="mt-1 h-2 w-full accent-magenta"
            />
          </label>
          <button
            type="button"
            onClick={() => apply(patchClip(present, selected.id, { rotate: nextRotate(selected.rotate) }))}
            className="mt-4 h-10 rounded-xl border border-line text-sm font-semibold"
          >
            {t('videos.editor.rotate')} {selected.rotate}°
          </button>
        </div>
        <label className="block text-xs font-medium text-muted">
          {t('videos.editor.panX')}
          <input
            type="range"
            aria-label={t('videos.editor.panX')}
            min={-1}
            max={1}
            step={0.05}
            value={frame.panX}
            onChange={(event) =>
              apply(
                patchClip(present, selected.id, {
                  crop: cropFromFrame(frame.zoom, Number(event.target.value), frame.panY),
                }),
              )
            }
            className="mt-1 h-2 w-full accent-teal"
          />
        </label>

        <div>
          <p className="text-xs font-medium text-muted">{t('videos.editor.transition')}</p>
          <div className="mt-1 grid grid-cols-3 gap-1">
            {TRANSITIONS.map((kind) => (
              <button
                key={kind}
                type="button"
                aria-pressed={selected.transition === kind}
                disabled={present.clips[0]?.id === selected.id}
                onClick={() => apply(patchClip(present, selected.id, { transition: kind }))}
                className={`h-10 rounded-xl text-xs font-semibold disabled:opacity-40 ${selected.transition === kind ? 'bg-plum text-cream' : 'border border-line'}`}
              >
                {transitionLabel(kind)}
              </button>
            ))}
          </div>
        </div>
      </section>

      <section className="space-y-2 rounded-3xl bg-surface p-4 shadow-card">
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold">{t('videos.editor.text')}</h2>
          <button
            type="button"
            onClick={() => {
              const start = Math.min(time, Math.max(0, duration - 1));
              const end = Math.min(duration, Math.max(start + 1.5, start + 0.4));
              apply(
                addText(present, {
                  id: uid(),
                  text: t('videos.editor.textSample'),
                  startSec: start,
                  endSec: end,
                  x: 0.5,
                  y: 0.78,
                }),
                t('videos.editor.tooShort'),
              );
            }}
            className="h-9 rounded-xl bg-accent px-3 text-sm font-semibold text-accent-fg"
          >
            {t('videos.editor.text')}
          </button>
        </div>
        {present.texts.map((cue) => (
          <div key={cue.id} className="space-y-2 rounded-2xl border border-line p-3">
            <input
              aria-label={t('videos.editor.text')}
              value={cue.text}
              onChange={(event) => apply(patchText(present, cue.id, { text: event.target.value }))}
              className="h-10 w-full rounded-xl border border-line bg-bg px-3 text-sm outline-none ring-accent focus:ring-2"
            />
            <div className="grid grid-cols-2 gap-2">
              <label className="text-xs text-muted">
                {t('videos.editor.textStart')}
                <input
                  value={formatTime(cue.startSec)}
                  onChange={(event) => {
                    const sec = parseClock(event.target.value);
                    if (sec === null) return;
                    apply(patchText(present, cue.id, { startSec: sec }));
                  }}
                  className="mt-1 h-10 w-full rounded-xl border border-line bg-bg px-3 text-sm text-fg"
                />
              </label>
              <label className="text-xs text-muted">
                {t('videos.editor.textEnd')}
                <input
                  value={formatTime(cue.endSec)}
                  onChange={(event) => {
                    const sec = parseClock(event.target.value);
                    if (sec === null) return;
                    apply(patchText(present, cue.id, { endSec: sec }));
                  }}
                  className="mt-1 h-10 w-full rounded-xl border border-line bg-bg px-3 text-sm text-fg"
                />
              </label>
            </div>
            <button
              type="button"
              onClick={() => apply(removeText(present, cue.id))}
              className="text-sm font-semibold text-danger"
            >
              {t('videos.editor.removeText')}
            </button>
          </div>
        ))}
      </section>

      <div className="grid grid-cols-2 gap-2">
        <button
          type="button"
          onClick={() => setPicker(picker === 'clip' ? null : 'clip')}
          className="h-11 rounded-2xl border border-line text-sm font-semibold"
        >
          {t('videos.editor.addClip')}
        </button>
        <button
          type="button"
          onClick={() => setPicker(picker === 'music' ? null : 'music')}
          className="h-11 rounded-2xl border border-line text-sm font-semibold"
        >
          {t('videos.editor.addMusic')}
        </button>
      </div>
      {present.music && (
        <button
          type="button"
          onClick={() => apply(clearMusic(present))}
          className="h-10 w-full rounded-2xl text-sm font-semibold text-danger"
        >
          {t('videos.editor.clearMusic')}
        </button>
      )}
      {picker && (
        <ul className="space-y-1 rounded-3xl bg-surface p-2 shadow-card">
          {doneJobs.length === 0 && <li className="px-3 py-2 text-sm text-muted">{t('videos.editor.emptyLibrary')}</li>}
          {doneJobs.map((item) => (
            <li key={item.id}>
              <button
                type="button"
                onClick={() => takeFromLibrary(item)}
                className="h-11 w-full rounded-2xl px-3 text-left text-sm font-semibold hover:bg-surface-2"
              >
                {picker === 'music'
                  ? t('videos.editor.useMusic', { title: item.title?.trim() || t('videos.untitled') })
                  : t('videos.editor.useClip', { title: item.title?.trim() || t('videos.untitled') })}
              </button>
            </li>
          ))}
        </ul>
      )}

      <form
        className="space-y-2"
        onSubmit={(event) => {
          event.preventDefault();
          exportVideo();
        }}
      >
        <label htmlFor="export-title" className="px-1 text-sm font-semibold">
          {t('videos.editor.exportTitle')}
        </label>
        <input
          id="export-title"
          value={exportName}
          onChange={(event) => setExportName(event.target.value)}
          maxLength={120}
          className="h-12 w-full rounded-2xl border border-line bg-surface px-3 text-sm outline-none ring-accent focus:ring-2"
        />
        <p className="px-1 text-xs text-muted">{t('videos.editor.exportHint')}</p>
        <button
          type="submit"
          disabled={compose.isPending}
          className="h-12 w-full rounded-2xl bg-plum text-sm font-semibold text-cream disabled:opacity-70"
        >
          {compose.isPending ? t('videos.editor.exporting') : t('videos.editor.export')}
        </button>
      </form>

      {hint && (
        <p role="alert" className="px-1 text-sm text-danger">
          {hint}
        </p>
      )}
    </div>
  );

  function takeFromLibrary(item: VideoJob) {
    if (picker === 'music') {
      apply(
        setMusic(present!, { jobId: item.id, title: item.title?.trim() || '', inSec: 0, volume: 0.6, offsetSec: 0 }),
      );
    } else {
      const clip = clipFromJob(item, uid());
      apply(clip ? addClip(present!, clip) : null, t('videos.editor.clipLimit'));
    }
    setPicker(null);
    haptic('light');
  }
}
