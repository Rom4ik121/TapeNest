import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { playerStore, usePlayer } from '@/entities/player';
import type { Track } from '@/shared/api/types';
import { AddToPlaylistSheet } from './AddToPlaylistSheet';
import { TrackRow } from './TrackRow';

interface Props {
  tracks: Track[];
  hasMore?: boolean;
  loadingMore?: boolean;
  onLoadMore?: () => void;
  /** Extra actions for the "more" sheet (e.g. remove from playlist). */
  onRemove?: (track: Track) => void;
  removeLabel?: string;
}

/**
 * Track list with infinite scroll: an IntersectionObserver sentinel calls
 * `onLoadMore` (follows nextCursor). Tapping a track plays it in the context
 * of every loaded track.
 */
export function TrackList({ tracks, hasMore, loadingMore, onLoadMore, onRemove, removeLabel }: Props) {
  const { t } = useTranslation();
  const currentId = usePlayer((s) => s.queue[s.index]?.id ?? null);
  const playing = usePlayer((s) => s.status === 'playing');
  const [menuTrack, setMenuTrack] = useState<Track | null>(null);
  const sentinel = useRef<HTMLDivElement | null>(null);

  const tracksRef = useRef(tracks);
  tracksRef.current = tracks;

  const onPlay = useCallback((track: Track) => {
    const st = playerStore.getState();
    if (st.queue[st.index]?.id === track.id && st.status !== 'idle' && st.status !== 'error') {
      st.toggle();
      return;
    }
    st.playTrack(track, tracksRef.current);
  }, []);
  const onLike = useCallback((track: Track) => playerStore.getState().setLiked(track.id, !track.liked), []);
  const onMore = useCallback((track: Track) => setMenuTrack(track), []);

  useEffect(() => {
    const el = sentinel.current;
    if (!el || !hasMore || !onLoadMore || typeof IntersectionObserver === 'undefined') return;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting) && !loadingMore) onLoadMore();
      },
      { rootMargin: '400px 0px' },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [hasMore, loadingMore, onLoadMore]);

  return (
    <>
      <ul className="space-y-0.5">
        {tracks.map((tr) => (
          <TrackRow
            key={tr.id}
            track={tr}
            active={tr.id === currentId}
            playing={playing}
            onPlay={onPlay}
            onLike={onLike}
            onMore={onMore}
          />
        ))}
      </ul>
      {hasMore && (
        <div ref={sentinel} className="py-4 text-center text-sm text-muted">
          {loadingMore ? (
            t('list.loadingMore')
          ) : (
            <button type="button" onClick={onLoadMore} className="rounded-full px-4 py-2 font-medium text-accent-ink">
              {t('list.loadingMore')}
            </button>
          )}
        </div>
      )}
      <AddToPlaylistSheet
        track={menuTrack}
        onClose={() => setMenuTrack(null)}
        onRemove={onRemove}
        removeLabel={removeLabel}
      />
    </>
  );
}
