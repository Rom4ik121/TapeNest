import { Mic2, Play } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router-dom';
import { AlbumCard, CardRow } from '@/entities/catalog/Cards';
import { useArtist } from '@/entities/catalog/queries';
import { playerStore } from '@/entities/player';
import { TrackList } from '@/entities/track/TrackList';
import { Cover } from '@/shared/ui/Cover';
import { SectionHeader } from '@/shared/ui/SectionHeader';
import { TrackListSkeleton } from '@/shared/ui/Skeleton';
import { EmptyState, ErrorState } from '@/shared/ui/States';

export function ArtistPage() {
  const { t } = useTranslation();
  const id = decodeURIComponent(useParams().artistId ?? '');
  const q = useArtist(id);

  if (q.isPending) return <TrackListSkeleton />;
  if (q.isError)
    return (
      <ErrorState
        error={q.error}
        title={t('artist.notFound')}
        onRetry={() => void q.refetch()}
        retrying={q.isFetching}
      />
    );
  const { artist, albums, tracks } = q.data;

  return (
    <div>
      <div className="flex flex-col items-center px-2 pt-4 text-center">
        <Cover id={artist.id} src={artist.coverUrl} alt="" className="h-36 w-36 shadow-glow" rounded="rounded-full" />
        <h1 className="mt-4 break-words text-2xl font-extrabold tracking-tight">{artist.name}</h1>
        {tracks.length > 0 && (
          <button
            type="button"
            onClick={() => playerStore.getState().playQueue(tracks, 0)}
            className="mt-4 inline-flex h-11 items-center gap-2 rounded-full bg-accent px-6 font-semibold text-accent-fg active:scale-95"
          >
            <Play className="h-4 w-4 fill-current" aria-hidden /> {t('artist.play')}
          </button>
        )}
      </div>
      {tracks.length === 0 && albums.length === 0 && (
        <EmptyState icon={<Mic2 className="h-10 w-10 opacity-40" aria-hidden />}>{t('artist.empty')}</EmptyState>
      )}
      {tracks.length > 0 && (
        <>
          <SectionHeader title={t('artist.popular')} />
          <TrackList tracks={tracks} />
        </>
      )}
      {albums.length > 0 && (
        <>
          <SectionHeader title={t('artist.albums')} />
          <CardRow>
            {albums.map((a) => (
              <AlbumCard key={a.id} album={a} />
            ))}
          </CardRow>
        </>
      )}
    </div>
  );
}
