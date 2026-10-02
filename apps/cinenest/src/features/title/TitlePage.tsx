import { Bookmark, BookmarkCheck, Play, Star } from 'lucide-react';
import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate, useParams } from 'react-router-dom';
import { pickDefaultFile, resumeAt } from '@/entities/title/files';
import { useContinueWatching, usePosition, useTitle, useToggleWatchlist } from '@/entities/title/queries';
import { cn } from '@/shared/lib/cn';
import { formatRating, formatTime } from '@/shared/lib/format';
import { useMainButton } from '@/shared/lib/useMainButton';
import { haptic } from '@/shared/telegram';
import { Poster } from '@/shared/ui/Poster';
import { Skeleton } from '@/shared/ui/Skeleton';
import { ErrorState } from '@/shared/ui/States';
import { FilePicker } from './FilePicker';

export function TitlePage() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const { titleId = '' } = useParams();
  const title = useTitle(titleId);
  const cont = useContinueWatching();
  const toggle = useToggleWatchlist();
  const [picked, setPicked] = useState<string | null>(null);

  const lastFileId = cont.data?.find((c) => c.title.id === titleId)?.file.id ?? null;
  const fileId = useMemo(
    () => picked ?? (title.data ? (pickDefaultFile(title.data.files, lastFileId)?.id ?? null) : null),
    [picked, title.data, lastFileId],
  );
  const pos = usePosition(titleId, fileId);
  const resume = resumeAt(pos.data);
  const cta = resume > 0 ? t('title.continueFrom', { time: formatTime(resume) }) : t('title.watch');

  const watch = (): void => {
    if (fileId) navigate(`/watch/${encodeURIComponent(titleId)}/${encodeURIComponent(fileId)}`);
  };
  const native = useMainButton(title.data && fileId ? { text: cta } : null, watch);

  if (title.isPending) {
    return (
      <div role="status" aria-busy="true" aria-label={t('a11y.skeleton')}>
        <Skeleton className="-mx-4 -mt-3 aspect-video rounded-none" />
        <div className="-mt-16 flex gap-4 px-1">
          <Skeleton className="aspect-[2/3] w-28 rounded-2xl" />
          <div className="flex-1 space-y-2 pt-16">
            <Skeleton className="h-6 w-4/5" />
            <Skeleton className="h-4 w-1/2" />
          </div>
        </div>
        <Skeleton className="mt-6 h-20 w-full" />
      </div>
    );
  }
  if (title.isError) {
    return (
      <ErrorState
        error={title.error}
        title={t('title.notFound')}
        onRetry={() => void title.refetch()}
        retrying={title.isFetching}
      />
    );
  }
  const d = title.data;
  const rating = formatRating(d.rating, i18n.language);

  return (
    <article>
      <div className="relative -mx-4 -mt-3">
        <Poster id={d.id} src={d.backdropUrl} alt="" aspect="wide" />
        <div className="absolute inset-0 bg-gradient-to-t from-bg via-bg/30 to-transparent" />
      </div>
      <div className="relative -mt-20 flex items-end gap-4 px-1">
        <Poster
          id={d.id}
          src={d.posterUrl}
          alt={t('a11y.poster', { title: d.title })}
          className="w-28 rounded-2xl shadow-card ring-1 ring-fg/10"
        />
        <div className="min-w-0 flex-1 pb-1">
          <h1 className="text-2xl font-extrabold leading-tight tracking-tight">{d.title}</h1>
          {d.originalTitle && <p className="truncate text-sm text-muted">{d.originalTitle}</p>}
          <p className="mt-1 flex flex-wrap items-center gap-x-2 text-xs font-semibold text-muted">
            <span>{t(`title.${d.kind}`)}</span>
            {d.year && <span>· {d.year}</span>}
            {d.runtimeMin && <span>· {t('title.minutes', { count: d.runtimeMin })}</span>}
            {rating && (
              <span className="inline-flex items-center gap-1" aria-label={t('title.rating', { r: rating })}>
                · <Star className="h-3 w-3 fill-amber text-amber" aria-hidden /> {rating}
              </span>
            )}
          </p>
        </div>
      </div>

      <div className="mt-4 flex flex-wrap gap-1.5 px-1">
        {d.genres.map((g) => (
          <span key={g} className="rounded-full bg-fg/[0.07] px-2.5 py-1 text-xs font-semibold">
            {t(`genre.${g}`, { defaultValue: g })}
          </span>
        ))}
      </div>

      <div className="mt-4 flex gap-2">
        {!native && (
          <button
            type="button"
            onClick={() => {
              haptic('medium');
              watch();
            }}
            disabled={!fileId}
            className="inline-flex min-h-[48px] flex-1 items-center justify-center gap-2 rounded-2xl bg-accent px-4 font-bold text-accent-fg shadow-glow transition active:scale-[0.98] disabled:opacity-50"
          >
            <Play className="h-5 w-5 fill-current" aria-hidden /> {cta}
          </button>
        )}
        <button
          type="button"
          aria-pressed={d.inWatchlist}
          onClick={() => {
            haptic(d.inWatchlist ? 'light' : 'success');
            toggle.mutate({ id: d.id, on: !d.inWatchlist });
          }}
          className={cn(
            'inline-flex min-h-[48px] items-center justify-center gap-2 rounded-2xl px-4 text-sm font-semibold transition active:scale-[0.98]',
            native && 'flex-1',
            d.inWatchlist ? 'bg-accent/15 text-accent-ink' : 'bg-surface shadow-card',
          )}
        >
          {d.inWatchlist ? (
            <BookmarkCheck className="h-5 w-5" aria-hidden />
          ) : (
            <Bookmark className="h-5 w-5" aria-hidden />
          )}
          {d.inWatchlist ? t('title.inLater') : t('title.addLater')}
        </button>
      </div>

      <p className="mt-5 px-1 text-[15px] leading-relaxed text-fg/85">{d.description}</p>

      <FilePicker title={d} selectedId={fileId} onSelect={(f) => setPicked(f.id)} />
    </article>
  );
}
