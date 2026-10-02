import { ChevronRight, FolderPlus, Heart, ListMusic } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { usePlaylistMutations, usePlaylists, useTrackList } from '@/entities/track/queries';
import { gradientFor } from '@/shared/lib/format';
import { cn } from '@/shared/lib/cn';
import { useMainButton } from '@/shared/lib/useMainButton';
import { haptic } from '@/shared/telegram';
import { Sheet } from '@/shared/ui/Sheet';
import { SectionHeader } from '@/shared/ui/SectionHeader';
import { Skeleton } from '@/shared/ui/Skeleton';
import { EmptyState, ErrorState } from '@/shared/ui/States';

export function LibraryPage() {
  const { t } = useTranslation();
  const playlists = usePlaylists();
  const liked = useTrackList('liked');
  const { create } = usePlaylistMutations();
  const [creating, setCreating] = useState(false);
  const [title, setTitle] = useState('');

  const submit = (): void => {
    const v = title.trim();
    if (!v) return;
    create.mutate(v, {
      onSuccess: () => {
        haptic('success');
        setTitle('');
        setCreating(false);
      },
      onError: () => haptic('error'),
    });
  };

  const nativeMain = useMainButton(
    creating
      ? {
          text: create.isPending ? t('library.creating') : t('library.create'),
          enabled: title.trim().length > 0 && !create.isPending,
          loading: create.isPending,
        }
      : null,
    submit,
  );

  return (
    <div>
      <h1 className="px-1 pt-2 text-[28px] font-extrabold tracking-tight">{t('library.title')}</h1>

      <Link
        to="/liked"
        className="scrim on-gradient relative mt-4 flex items-center gap-4 overflow-hidden rounded-4xl bg-grad-03 p-5 text-cream shadow-card active:scale-[0.99]"
      >
        <div className="absolute -right-8 -top-8 h-32 w-32 rounded-full bg-cream/15 blur-xl" />
        <span className="relative grid h-14 w-14 place-items-center rounded-2xl bg-ink/20">
          <Heart className="h-7 w-7 fill-current" aria-hidden />
        </span>
        <div className="relative flex-1">
          <p className="text-xl font-extrabold">{t('library.liked')}</p>
          <p className="text-sm text-cream/85">
            {liked.isSuccess ? (
              t('library.tracks', { count: liked.tracks.length })
            ) : (
              <span aria-hidden className="mt-1 inline-block h-3 w-16 animate-pulse rounded-lg bg-cream/25" />
            )}
          </p>
        </div>
        <ChevronRight className="relative h-6 w-6" aria-hidden />
      </Link>

      <SectionHeader
        title={t('library.playlists')}
        action={
          <button
            type="button"
            onClick={() => {
              haptic('light');
              setCreating(true);
            }}
            className="inline-flex min-h-[36px] items-center gap-1.5 rounded-full bg-accent px-3 py-1.5 text-sm font-semibold text-accent-fg transition active:scale-95"
          >
            <FolderPlus className="h-4 w-4" aria-hidden /> {t('library.newPlaylist')}
          </button>
        }
      />

      {playlists.isPending && (
        <div className="grid grid-cols-2 gap-3">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-40 rounded-3xl" />
          ))}
        </div>
      )}
      {playlists.isError && (
        <ErrorState error={playlists.error} onRetry={() => void playlists.refetch()} retrying={playlists.isFetching} />
      )}
      {playlists.isSuccess && playlists.data.length === 0 && (
        <EmptyState icon={<ListMusic className="h-10 w-10 opacity-40" aria-hidden />}>{t('library.empty')}</EmptyState>
      )}
      {playlists.isSuccess && playlists.data.length > 0 && (
        <div className="grid grid-cols-2 gap-3">
          {playlists.data.map((p) => (
            <Link
              key={p.id}
              to={`/library/${encodeURIComponent(p.id)}`}
              onClick={() => haptic('selection')}
              className="group rounded-3xl bg-surface p-2.5 shadow-card transition active:scale-[0.98]"
            >
              <div
                className={cn(
                  'relative grid aspect-square place-items-center overflow-hidden rounded-2xl text-cream',
                  gradientFor(p.id),
                )}
              >
                <ListMusic className="h-10 w-10 opacity-90" aria-hidden />
                <div className="absolute -bottom-6 -right-6 h-20 w-20 rounded-full bg-cream/15" />
              </div>
              <p className="mt-2 truncate px-1 font-semibold">{p.title}</p>
              <p className="px-1 text-xs text-muted">{t('library.tracks', { count: p.trackCount })}</p>
            </Link>
          ))}
        </div>
      )}

      <Sheet open={creating} onClose={() => setCreating(false)} title={t('library.newPlaylist')}>
        <form
          className="space-y-3 pb-3"
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <input
            autoFocus
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            maxLength={100}
            placeholder={t('library.namePlaceholder')}
            aria-label={t('library.nameLabel')}
            enterKeyHint="done"
            className="h-12 w-full rounded-2xl bg-fg/5 px-4 text-[16px] outline-none placeholder:text-muted focus:ring-2 focus:ring-accent/50"
          />
          {!nativeMain && (
            <div className="flex gap-2">
              <button
                type="button"
                onClick={() => setCreating(false)}
                className="h-12 flex-1 rounded-2xl bg-fg/5 font-semibold"
              >
                {t('library.cancel')}
              </button>
              <button
                type="submit"
                disabled={!title.trim() || create.isPending}
                className="h-12 flex-1 rounded-2xl bg-accent font-semibold text-accent-fg disabled:opacity-40"
              >
                {create.isPending ? t('library.creating') : t('library.create')}
              </button>
            </div>
          )}
        </form>
      </Sheet>
    </div>
  );
}
