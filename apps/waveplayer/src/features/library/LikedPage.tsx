import { Heart, Play, Shuffle } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { playerStore } from '@/entities/player';
import { useTrackList } from '@/entities/track/queries';
import { TrackList } from '@/entities/track/TrackList';
import { tracksApi } from '@/shared/api/endpoints';
import type { Track } from '@/shared/api/types';
import { shuffled } from '@/shared/lib/shuffle';
import { TrackListSkeleton } from '@/shared/ui/Skeleton';
import { EmptyState, ErrorState } from '@/shared/ui/States';

export function LikedPage() {
  const { t } = useTranslation();
  const liked = useTrackList('liked');
  const [shuffling, setShuffling] = useState(false);

  const playShuffled = async (): Promise<void> => {
    if (shuffling) return;
    setShuffling(true);
    let tracks: Track[] = liked.tracks;
    try {
      if (liked.hasNextPage) tracks = await tracksApi.listAll('liked');
    } catch {
      tracks = liked.tracks;
    } finally {
      setShuffling(false);
    }
    if (tracks.length === 0) return;
    playerStore.getState().playQueue(shuffled(tracks), 0);
  };

  return (
    <div>
      <div className="scrim on-gradient relative -mx-4 -mt-3 mb-4 overflow-hidden bg-grad-03 px-5 pb-6 pt-10 text-cream">
        <div className="absolute -right-10 top-0 h-40 w-40 rounded-full bg-cream/15 blur-2xl" />
        <Heart className="relative h-10 w-10 fill-current" />
        <h1 className="relative mt-3 text-3xl font-extrabold tracking-tight">{t('library.liked')}</h1>
        <div className="relative mt-3 flex items-center justify-between">
          <p className="text-sm text-cream/85">
            {liked.isSuccess ? t('library.tracks', { count: liked.tracks.length }) : ' '}
          </p>
          {liked.tracks.length > 0 && (
            <div className="flex items-center gap-2">
              <button
                type="button"
                disabled={shuffling}
                onClick={() => void playShuffled()}
                aria-label={t('library.shuffle')}
                className="grid h-14 w-14 place-items-center rounded-full bg-cream/20 text-cream disabled:opacity-60 active:scale-90"
              >
                <Shuffle className="h-5 w-5" aria-hidden />
              </button>
              <button
                type="button"
                onClick={() => playerStore.getState().playQueue(liked.tracks, 0)}
                aria-label={t('playlist.play')}
                className="grid h-14 w-14 place-items-center rounded-full bg-cream text-ink shadow-lg active:scale-90"
              >
                <Play className="h-6 w-6 translate-x-[1px] fill-current" />
              </button>
            </div>
          )}
        </div>
      </div>
      {liked.isPending && <TrackListSkeleton />}
      {liked.isError && (
        <ErrorState error={liked.error} onRetry={() => void liked.refetch()} retrying={liked.isFetching} />
      )}
      {liked.isSuccess && liked.tracks.length === 0 && <EmptyState>{t('list.empty')}</EmptyState>}
      {liked.tracks.length > 0 && (
        <TrackList
          tracks={liked.tracks}
          hasMore={liked.hasNextPage}
          loadingMore={liked.isFetchingNextPage}
          onLoadMore={() => void liked.fetchNextPage()}
        />
      )}
    </div>
  );
}
