import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate, useParams } from 'react-router-dom';
import { downloadErrorKey } from '@/entities/video/api';
import { useDeleteVideo, useRenameVideo, useTrimVideo, useVideo, useVideoFile } from '@/entities/video/queries';
import { formatWhen, trimRange } from '@/entities/video/range';
import { formatTime } from '@/shared/lib/format';
import { haptic } from '@/shared/telegram';
import { ErrorState } from '@/shared/ui/States';

export function VideoPage() {
  const { id = '' } = useParams();
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const video = useVideo(id);
  const job = video.data;
  const file = useVideoFile(id, job?.status === 'done');
  const rename = useRenameVideo(id);
  const remove = useDeleteVideo();
  const trim = useTrimVideo(id);

  const [title, setTitle] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [start, setStart] = useState('0:00');
  const [end, setEnd] = useState('');
  const [trimError, setTrimError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  if (video.isPending) {
    return <div className="h-48 animate-pulse rounded-3xl bg-surface-2" aria-busy="true" />;
  }
  if (video.isError || !job) {
    return <ErrorState error={video.error} onRetry={() => void video.refetch()} title={t('videos.notFound')} />;
  }

  const shownTitle = title ?? job.title ?? '';
  const duration = job.file?.durationSec ?? 0;
  const when = formatWhen(job.finishedAt ?? job.createdAt, i18n.language);

  const saveTitle = () => {
    setActionError(null);
    haptic('light');
    rename.mutate(shownTitle, { onError: (error) => setActionError(t(downloadErrorKey(error))) });
  };

  const doDelete = () => {
    setActionError(null);
    haptic('medium');
    remove.mutate(id, {
      onSuccess: () => navigate('/videos', { replace: true }),
      onError: (error) => setActionError(t(downloadErrorKey(error))),
    });
  };

  const doTrim = () => {
    const range = trimRange(start, end || (duration > 0 ? formatTime(duration) : ''), duration);
    if (!range) {
      setTrimError(t('videos.errors.range'));
      return;
    }
    setTrimError(null);
    setActionError(null);
    haptic('medium');
    trim.mutate(range, {
      onSuccess: (created) => navigate(`/videos/${created.id}`, { replace: true }),
      onError: (error) => setActionError(t(downloadErrorKey(error))),
    });
  };

  return (
    <div>
      <h1 className="px-1 text-xl font-extrabold leading-tight">{job.title?.trim() || t('videos.untitled')}</h1>
      <p className="mt-1 px-1 text-xs text-muted">
        {duration > 0 ? t('videos.duration', { time: formatTime(duration) }) : null}
        {duration > 0 && when ? ' · ' : ''}
        {when}
      </p>

      <div className="mt-4 overflow-hidden rounded-3xl bg-ink shadow-card">
        {job.status === 'done' && file.data?.url ? (
          <video
            key={file.data.url}
            src={file.data.url}
            poster={job.posterUrl}
            controls
            playsInline
            className="aspect-video w-full bg-ink"
            aria-label={t('videos.play')}
          />
        ) : (
          <div className="grid aspect-video place-items-center px-6 text-center text-sm text-cream/80">
            {job.status === 'failed'
              ? t('videos.status.failed')
              : t('videos.status.running', { pct: Math.round(job.progress?.pct ?? 0) })}
          </div>
        )}
      </div>

      {job.status === 'done' && (
        <div className="mt-5 space-y-5">
          <form
            className="space-y-2"
            onSubmit={(event) => {
              event.preventDefault();
              saveTitle();
            }}
          >
            <label htmlFor="video-title" className="px-1 text-sm font-semibold">
              {t('videos.rename')}
            </label>
            <div className="flex gap-2">
              <input
                id="video-title"
                value={shownTitle}
                onChange={(event) => setTitle(event.target.value)}
                maxLength={120}
                className="h-12 min-w-0 flex-1 rounded-2xl border border-line bg-surface px-3 text-sm outline-none ring-accent focus:ring-2"
              />
              <button
                type="submit"
                disabled={rename.isPending}
                className="h-12 rounded-2xl bg-accent px-4 text-sm font-semibold text-accent-fg disabled:opacity-70"
              >
                {rename.isPending ? t('videos.saving') : t('videos.save')}
              </button>
            </div>
          </form>

          <section className="rounded-3xl bg-surface p-4 shadow-card">
            <h2 className="text-sm font-semibold">{t('videos.trim')}</h2>
            <p className="mt-1 text-xs text-muted">{t('videos.trimHint')}</p>
            <div className="mt-3 grid grid-cols-2 gap-2">
              <label className="text-xs font-medium text-muted">
                {t('videos.start')}
                <input
                  value={start}
                  onChange={(event) => setStart(event.target.value)}
                  inputMode="numeric"
                  className="mt-1 h-11 w-full rounded-xl border border-line bg-bg px-3 text-sm text-fg outline-none ring-accent focus:ring-2"
                />
              </label>
              <label className="text-xs font-medium text-muted">
                {t('videos.end')}
                <input
                  value={end}
                  onChange={(event) => setEnd(event.target.value)}
                  placeholder={duration > 0 ? formatTime(duration) : '0:00'}
                  inputMode="numeric"
                  className="mt-1 h-11 w-full rounded-xl border border-line bg-bg px-3 text-sm text-fg outline-none ring-accent focus:ring-2"
                />
              </label>
            </div>
            {trimError && (
              <p role="alert" className="mt-2 text-sm text-danger">
                {trimError}
              </p>
            )}
            <button
              type="button"
              onClick={doTrim}
              disabled={trim.isPending}
              className="mt-3 h-11 w-full rounded-2xl bg-plum text-sm font-semibold text-cream disabled:opacity-70"
            >
              {trim.isPending ? t('videos.trimming') : t('videos.trimSave')}
            </button>
          </section>

          {confirming ? (
            <div className="rounded-3xl border border-danger/30 bg-surface p-4">
              <p className="text-sm">{t('videos.confirmDelete')}</p>
              <div className="mt-3 flex gap-2">
                <button
                  type="button"
                  onClick={() => setConfirming(false)}
                  className="h-11 flex-1 rounded-2xl border border-line text-sm font-semibold"
                >
                  {t('videos.cancel')}
                </button>
                <button
                  type="button"
                  onClick={doDelete}
                  disabled={remove.isPending}
                  className="h-11 flex-1 rounded-2xl bg-danger text-sm font-semibold text-danger-fg disabled:opacity-70"
                >
                  {remove.isPending ? t('videos.deleting') : t('videos.delete')}
                </button>
              </div>
            </div>
          ) : (
            <button
              type="button"
              onClick={() => setConfirming(true)}
              className="h-11 w-full rounded-2xl text-sm font-semibold text-danger"
            >
              {t('videos.delete')}
            </button>
          )}
        </div>
      )}

      {actionError && (
        <p role="alert" className="mt-3 px-1 text-sm text-danger">
          {actionError}
        </p>
      )}
    </div>
  );
}
