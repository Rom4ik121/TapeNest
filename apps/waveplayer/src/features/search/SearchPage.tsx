import { Search, X } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { AlbumCard, ArtistCard, CardRow } from '@/entities/catalog/Cards';
import { useCatalogSearch } from '@/entities/catalog/queries';
import { TrackList } from '@/entities/track/TrackList';
import { useDebouncedValue } from '@/shared/lib/useDebouncedValue';
import { SectionHeader } from '@/shared/ui/SectionHeader';
import { TrackListSkeleton } from '@/shared/ui/Skeleton';
import { EmptyState, ErrorState } from '@/shared/ui/States';
import { haptic } from '@/shared/telegram';

const TRACKS_PREVIEW = 6;

/**
 * Unified search: the library and the wider catalog in one list. Every result
 * looks and behaves the same — a track that is not on the server yet simply
 * plays after a short loading state (ADR 0011).
 */
export function SearchPage() {
  const { t } = useTranslation();
  const [q, setQ] = useState('');
  const [allTracks, setAllTracks] = useState(false);
  const debounced = useDebouncedValue(q, 350);
  const search = useCatalogSearch(debounced);
  const term = debounced.trim();
  const data = search.data;
  const empty = !!data && data.tracks.length + data.albums.length + data.artists.length === 0;
  const tracks = data ? (allTracks ? data.tracks : data.tracks.slice(0, TRACKS_PREVIEW)) : [];

  return (
    <div>
      <h1 className="px-1 pt-2 text-[28px] font-extrabold tracking-tight">{t('search.title')}</h1>
      <label className="sticky top-2 z-10 mt-3 flex items-center gap-2 rounded-2xl bg-surface px-3 shadow-card ring-1 ring-fg/5 focus-within:ring-2 focus-within:ring-accent/50">
        <Search className="h-5 w-5 text-muted" aria-hidden />
        <input
          type="search"
          value={q}
          onChange={(e) => {
            setQ(e.target.value);
            setAllTracks(false);
          }}
          placeholder={t('search.placeholder')}
          aria-label={t('search.placeholder')}
          enterKeyHint="search"
          className="h-12 min-w-0 flex-1 bg-transparent text-[16px] outline-none placeholder:text-muted"
        />
        {q && (
          <button
            type="button"
            onClick={() => {
              haptic('selection');
              setQ('');
            }}
            aria-label={t('search.clear')}
            className="grid h-8 w-8 place-items-center rounded-full text-muted transition hover:bg-fg/5 active:scale-90"
          >
            <X className="h-4 w-4" aria-hidden />
          </button>
        )}
      </label>

      <div className="mt-2">
        {!term && (
          <div className="mt-4">
            <EmptyState icon={<Search className="h-10 w-10 opacity-40" aria-hidden />}>{t('search.prompt')}</EmptyState>
          </div>
        )}
        {term && search.isPending && (
          <div className="mt-4">
            <TrackListSkeleton />
          </div>
        )}
        {term && search.isError && !data && (
          <ErrorState error={search.error} onRetry={() => void search.refetch()} retrying={search.isFetching} />
        )}
        {term && empty && <EmptyState>{t('search.noResults', { q: term })}</EmptyState>}
        {term && data && data.tracks.length > 0 && (
          <section aria-label={t('search.tracks')}>
            <SectionHeader
              title={t('search.tracks')}
              action={
                data.tracks.length > TRACKS_PREVIEW ? (
                  <button
                    type="button"
                    onClick={() => setAllTracks((v) => !v)}
                    className="rounded-full px-2 py-1 text-sm font-semibold text-accent-ink"
                  >
                    {allTracks ? t('search.showLess') : t('search.showAll')}
                  </button>
                ) : undefined
              }
            />
            <TrackList tracks={tracks} />
          </section>
        )}
        {term && data && data.albums.length > 0 && (
          <section aria-label={t('search.albums')}>
            <SectionHeader title={t('search.albums')} />
            <CardRow>
              {data.albums.map((a) => (
                <AlbumCard key={a.id} album={a} />
              ))}
            </CardRow>
          </section>
        )}
        {term && data && data.artists.length > 0 && (
          <section aria-label={t('search.artists')}>
            <SectionHeader title={t('search.artists')} />
            <CardRow>
              {data.artists.map((a) => (
                <ArtistCard key={a.id} artist={a} />
              ))}
            </CardRow>
          </section>
        )}
      </div>
    </div>
  );
}
