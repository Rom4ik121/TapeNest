import { Disc3, Play, Shuffle } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Link, useParams } from 'react-router-dom';
import { useAlbum } from '@/entities/catalog/queries';
import { playerStore } from '@/entities/player';
import { TrackList } from '@/entities/track/TrackList';
import { shuffled } from '@/shared/lib/shuffle';
import { Cover } from '@/shared/ui/Cover';
import { TrackListSkeleton } from '@/shared/ui/Skeleton';
import { EmptyState, ErrorState } from '@/shared/ui/States';

/** Album screen (library or not-yet-available — same UI; playing acquires invisibly). */
export function AlbumPage() {
  const { t } = useTranslation();
  const id = decodeURIComponent(useParams().albumId ?? '');
  const q = useAlbum(id);

  if (q.isPending) return <TrackListSkeleton />;
  if (q.isError)
    return (
      <ErrorState
        error={q.error}
        title={t('album.notFound')}
        onRetry={() => void q.refetch()}
        retrying={q.isFetching}
      />
    );
  const { album, tracks } = q.data;

  return (
    <div>
      <div className="flex flex-col items-center px-2 pt-4 text-center">
        <Cover id={album.id} src={album.coverUrl} alt="" className="h-48 w-48 shadow-glow" rounded="rounded-3xl" />
        <h1 className="mt-4 break-words text-2xl font-extrabold tracking-tight">{album.title}</h1>
        <p className="mt-1 text-sm text-muted">
          {album.artistId ? (
            <Link to={`/artist/${encodeURIComponent(album.artistId)}`} className="font-semibold text-fg">
              {album.artist}
            </Link>
          ) : (
            album.artist
          )}
          {album.year ? ` · ${album.year}` : ''}
        </p>
        <div className="mt-4 flex items-center gap-2">
          <button
            type="button"
            disabled={tracks.length === 0}
            onClick={() => playerStore.getState().playQueue(tracks, 0)}
            className="inline-flex h-11 items-center gap-2 rounded-full bg-accent px-6 font-semibold text-accent-fg disabled:opacity-50 active:scale-95"
          >
            <Play className="h-4 w-4 fill-current" aria-hidden /> {t('album.play')}
          </button>
          <button
            type="button"
            disabled={tracks.length === 0}
            onClick={() => playerStore.getState().playQueue(shuffled(tracks), 0)}
            aria-label={t('album.shuffle')}
            className="grid h-11 w-11 place-items-center rounded-full bg-fg/5 disabled:opacity-50 active:scale-95"
          >
            <Shuffle className="h-5 w-5" aria-hidden />
          </button>
        </div>
      </div>
      <div className="mt-6">
        {tracks.length === 0 ? (
          <EmptyState icon={<Disc3 className="h-10 w-10 opacity-40" aria-hidden />}>{t('album.empty')}</EmptyState>
        ) : (
          <TrackList tracks={tracks} />
        )}
      </div>
    </div>
  );
}
