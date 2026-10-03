import { Clapperboard, Link2 } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { downloadErrorKey } from '@/entities/video/api';
import { useCreateDownload, useVideoList } from '@/entities/video/queries';
import { formatWhen } from '@/entities/video/range';
import type { VideoJob } from '@/entities/video/types';
import { formatTime, gradientFor } from '@/shared/lib/format';
import { haptic } from '@/shared/telegram';
import { EmptyState, ErrorState } from '@/shared/ui/States';

const SOURCE_LABEL: Record<string, string> = {
  youtube: 'YouTube',
  vk: 'VK',
  rutube: 'RuTube',
  tiktok: 'TikTok',
  vimeo: 'Vimeo',
  dailymotion: 'Dailymotion',
  instagram: 'Instagram',
  twitter: 'X',
  twitch: 'Twitch',
  facebook: 'Facebook',
  ok: 'OK',
  coub: 'Coub',
  reddit: 'Reddit',
  streamable: 'Streamable',
  rumble: 'Rumble',
  kick: 'Kick',
  bilibili: 'Bilibili',
  mailru: 'Mail.ru',
  niconico: 'Niconico',
  telegram: 'Telegram',
};

function sourceLabel(source: string): string {
  return SOURCE_LABEL[source] ?? source;
}

function hostOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, '');
  } catch {
    return '';
  }
}

/** A finished file. Failed rows with nothing stored are not videos. */
function isLibraryVideo(job: VideoJob): boolean {
  return job.status === 'done' || Boolean(job.file);
}

function VideoCard({ job }: { job: VideoJob }) {
  const { t, i18n } = useTranslation();
  const title = job.title?.trim() || t('videos.untitled');
  const when = formatWhen(job.finishedAt ?? job.createdAt, i18n.language);
  const duration = job.file?.durationSec ?? 0;

  return (
    <Link
      to={`/videos/${job.id}`}
      onClick={() => haptic('light')}
      aria-label={t('videos.open', { title })}
      className="group block overflow-hidden rounded-3xl bg-surface shadow-card transition active:scale-[0.99]"
    >
      <div className={`relative aspect-video ${job.posterUrl ? 'bg-ink' : gradientFor(job.id)}`}>
        {job.posterUrl ? (
          <img src={job.posterUrl} alt="" className="h-full w-full object-cover" />
        ) : (
          <div className="grid h-full place-items-center text-cream/90">
            <Clapperboard className="h-8 w-8" aria-hidden />
          </div>
        )}
        {duration > 0 && (
          <span className="absolute bottom-2 right-2 rounded-full bg-ink/75 px-2 py-0.5 text-[11px] font-semibold text-cream">
            {formatTime(duration)}
          </span>
        )}
      </div>
      <div className="space-y-1 px-3 py-3">
        <p className="line-clamp-2 text-[15px] font-semibold leading-snug">{title}</p>
        <p className="text-xs text-muted">
          {sourceLabel(job.source)}
          {when ? ` · ${when}` : ''}
        </p>
      </div>
    </Link>
  );
}

function ActiveDownload({ job }: { job: VideoJob }) {
  const { t } = useTranslation();
  const where = hostOf(job.url) || sourceLabel(job.source);
  return (
    <div className="flex items-center justify-between gap-3 rounded-2xl border border-line bg-surface px-3 py-2.5">
      <p className="min-w-0 truncate text-sm text-muted">{where}</p>
      <p className="shrink-0 text-xs font-semibold text-highlight">
        {job.status === 'running'
          ? t('videos.status.running', { pct: Math.round(job.progress?.pct ?? 0) })
          : t('videos.status.queued')}
      </p>
    </div>
  );
}

function LibraryList({ items, empty }: { items: VideoJob[]; empty: string }) {
  const videos = items.filter(isLibraryVideo);
  const active = items.filter((job) => job.status === 'queued' || job.status === 'running');
  if (videos.length === 0 && active.length === 0) {
    return <EmptyState icon={<Clapperboard className="h-8 w-8" aria-hidden />}>{empty}</EmptyState>;
  }
  return (
    <div className="grid gap-3">
      {active.length > 0 && (
        <ul className="grid gap-2">
          {active.map((job) => (
            <li key={job.id}>
              <ActiveDownload job={job} />
            </li>
          ))}
        </ul>
      )}
      {videos.length > 0 && (
        <ul className="grid gap-3">
          {videos.map((job) => (
            <li key={job.id}>
              <VideoCard job={job} />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function VideoLibraryPage() {
  const { t } = useTranslation();
  const list = useVideoList();
  const create = useCreateDownload();
  const [url, setUrl] = useState('');
  const [formError, setFormError] = useState<string | null>(null);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const next = url.trim();
    if (!next) {
      setFormError(t('videos.errors.invalid'));
      return;
    }
    setFormError(null);
    haptic('medium');
    create.mutate(next, {
      onSuccess: () => setUrl(''),
      onError: (error) => setFormError(t(downloadErrorKey(error))),
    });
  };

  return (
    <div>
      <header className="px-1 pt-2">
        <p className="text-sm font-medium text-muted">{t('videos.subtitle')}</p>
        <h1 className="mt-0.5 text-[28px] font-extrabold leading-tight tracking-tight">{t('videos.title')}</h1>
      </header>

      <form onSubmit={submit} className="mt-4 flex gap-2">
        <label className="sr-only" htmlFor="video-url">
          {t('videos.url')}
        </label>
        <div className="relative min-w-0 flex-1">
          <Link2 className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" aria-hidden />
          <input
            id="video-url"
            value={url}
            onChange={(event) => setUrl(event.target.value)}
            placeholder="https://"
            inputMode="url"
            autoCapitalize="none"
            autoCorrect="off"
            className="h-12 w-full rounded-2xl border border-line bg-surface pl-10 pr-3 text-sm outline-none ring-accent focus:ring-2"
          />
        </div>
        <button
          type="submit"
          disabled={create.isPending}
          className="h-12 shrink-0 rounded-2xl bg-accent px-4 text-sm font-semibold text-accent-fg transition active:scale-95 disabled:opacity-70"
        >
          {create.isPending ? t('videos.downloading') : t('videos.download')}
        </button>
      </form>
      {formError && (
        <p role="alert" className="mt-2 px-1 text-sm text-danger">
          {formError}
        </p>
      )}

      <div className="mt-5">
        {list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} retrying={list.isRefetching} />}
        {list.isPending && (
          <div className="grid gap-3" aria-busy="true">
            <div className="h-40 animate-pulse rounded-3xl bg-surface-2" />
            <div className="h-40 animate-pulse rounded-3xl bg-surface-2" />
          </div>
        )}
        {list.isSuccess && <LibraryList items={list.data.items} empty={t('videos.empty')} />}
      </div>
    </div>
  );
}
