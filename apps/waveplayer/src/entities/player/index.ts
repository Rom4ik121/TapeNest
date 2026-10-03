import { useStore } from 'zustand';
import type { InfiniteData } from '@tanstack/react-query';
import { tracksApi, waveApi } from '@/shared/api/endpoints';
import { qk, queryClient } from '@/shared/api/queryClient';
import type { Page, PlaylistWithTracks, Track } from '@/shared/api/types';
import { ApiError } from '@/shared/api/client';
import { haptic } from '@/shared/telegram';
import { showToast } from '@/shared/ui/toastStore';
import { HtmlAudioEngine } from './audioEngine';
import { createPlayerStore, type PlayerStore } from './playerStore';
import { loadSession, saveSession } from './session';
import { warmStream } from './warmStream';

/** Flip `liked` for a track in every cached list so the UI stays consistent. */
function patchLikedInCache(trackId: string, liked: boolean): void {
  const flip = (t: Track): Track => (t.id === trackId ? { ...t, liked } : t);
  queryClient.setQueriesData<InfiniteData<Page<Track>>>({ queryKey: qk.tracks.root }, (data) =>
    data && 'pages' in data
      ? {
          ...data,
          pages: data.pages.map((p) => ({ ...p, items: p.items.map(flip) })),
        }
      : data,
  );
  queryClient.setQueriesData<PlaylistWithTracks>({ queryKey: qk.playlists.root }, (data) =>
    data && 'tracks' in data ? { ...data, tracks: data.tracks.map(flip) } : data,
  );
  // unified catalog views (search, album, artist) hold plain track arrays
  queryClient.setQueriesData<{ tracks?: Track[] }>({ queryKey: qk.catalog.root }, (data) =>
    data && Array.isArray(data.tracks) ? { ...data, tracks: data.tracks.map(flip) } : data,
  );
  void queryClient.invalidateQueries({ queryKey: qk.tracks.list('liked') });
}

/** i18n key of the toast for a failed start (null: silent, the player shows its error state). */
export function streamErrorKey(err: unknown): string | null {
  if (!(err instanceof ApiError)) return null;
  switch (err.code) {
    case 'NO_SOURCES':
      return 'player.noSources';
    case 'ACQUIRE_QUOTA':
      return 'player.quota';
    case 'NOT_AVAILABLE':
      return 'player.notAvailable';
    case 'ACQUIRE_TIMEOUT':
      return 'player.acquireTimeout';
    default:
      return null;
  }
}

export const playerStore = createPlayerStore({
  engine: new HtmlAudioEngine(),
  streamUrl: (id, signal) => tracksApi.streamUrl(id, signal),
  like: tracksApi.like,
  unlike: tracksApi.unlike,
  reportListened: tracksApi.reportListened,
  reportSkipped: tracksApi.reportSkipped,
  savePosition: (id, pos, keepalive) => tracksApi.savePosition(id, pos, keepalive),
  waveStart: waveApi.start,
  waveMore: waveApi.more,
  waveFeedback: waveApi.feedback,
  popular: () => tracksApi.list('popular', null),
  onLikeChanged: patchLikedInCache,
  onStreamError: (_id, err) => {
    const key = streamErrorKey(err);
    if (key) showToast(key);
  },
  persistSession: saveSession,
  haptic: (k) => haptic(k),
  warm: warmStream,
});

export function usePlayer<T>(selector: (s: PlayerStore) => T): T {
  return useStore(playerStore, selector);
}

export function useCurrentTrack(): Track | null {
  return usePlayer((s) => s.queue[s.index] ?? null);
}

/**
 * Restores the last session and installs lifecycle hooks that force-save the
 * playback position when the WebView is hidden or unloaded.
 */
export function startPlayerLifecycle(): () => void {
  const session = loadSession();
  if (session) {
    playerStore.getState().restore(session);
    const track = session.queue[session.index];
    // Another device may have a fresher position (server is the source of truth).
    if (track) {
      tracksApi
        .getPosition(track.id)
        .then((pos) => {
          const s = playerStore.getState();
          if (
            Date.parse(pos.updatedAt) > session.savedAt &&
            s.status === 'paused' &&
            s.queue[s.index]?.id === track.id
          ) {
            playerStore.setState({
              positionSec: pos.positionSec,
              resumeAtSec: pos.positionSec,
            });
          }
        })
        .catch(() => undefined);
    }
  }

  const onVisibility = (): void => {
    if (document.visibilityState === 'hidden') playerStore.getState().flushPosition(true);
  };
  const onPageHide = (): void => playerStore.getState().flushPosition(true);
  document.addEventListener('visibilitychange', onVisibility);
  window.addEventListener('pagehide', onPageHide);
  return () => {
    document.removeEventListener('visibilitychange', onVisibility);
    window.removeEventListener('pagehide', onPageHide);
  };
}

export type { PlayerStore, PlayerStatus } from './playerStore';
