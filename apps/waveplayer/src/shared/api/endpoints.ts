import { api } from './client';
import {
  albumViewWithMedia,
  artistViewWithMedia,
  pageWithMedia,
  playlistWithMedia,
  searchWithMedia,
  streamWithMedia,
  waveWithMedia,
  withMedia,
} from './media';
import { waitForStream } from './stream';
import type {
  AlbumView,
  ArtistView,
  AuthTokens,
  SearchResult,
  StreamPending,
  Page,
  PlaybackPosition,
  Playlist,
  PlaylistWithTracks,
  StreamUrl,
  Track,
  WaveFeedbackAction,
  WaveMode,
  User,
  WaveSession,
} from './types';

export const PAGE_SIZE = 20;

export const authApi = {
  login: (initData: string) =>
    api<AuthTokens>('/auth/telegram', {
      method: 'POST',
      body: { initData },
      auth: false,
    }),
  refresh: (refreshToken: string) =>
    api<AuthTokens>('/auth/refresh', {
      method: 'POST',
      body: { refreshToken },
      auth: false,
    }),
  logout: (refreshToken: string) =>
    api<void>('/auth/logout', {
      method: 'POST',
      body: { refreshToken },
      auth: false,
    }),
  /** Validates the session (401 → shared refresh → retry) and returns a fresh profile. */
  me: (signal?: AbortSignal) => api<User>('/me', { signal }),
};

export type TrackListKind = 'recent' | 'popular' | 'liked';

export const tracksApi = {
  list: (kind: TrackListKind, cursor: string | null, signal?: AbortSignal) =>
    api<Page<Track>>(`/tracks/${kind}`, {
      query: { cursor, limit: PAGE_SIZE },
      signal,
    }).then(pageWithMedia),
  search: (q: string, cursor: string | null, signal?: AbortSignal) =>
    api<Page<Track>>('/tracks/search', {
      query: { q, cursor, limit: PAGE_SIZE },
      signal,
    }).then(pageWithMedia),
  /** Playable URL; waits (202 + retry) while a not-yet-available track is fetched. */
  streamUrl: (trackId: string, signal?: AbortSignal) =>
    waitForStream(
      (sig) => api<StreamUrl | StreamPending>(`/tracks/${encodeURIComponent(trackId)}/stream-url`, { signal: sig }),
      signal,
    ).then(streamWithMedia),
  like: (trackId: string) =>
    api<void>(`/tracks/${encodeURIComponent(trackId)}/like`, {
      method: 'POST',
    }),
  unlike: (trackId: string) =>
    api<void>(`/tracks/${encodeURIComponent(trackId)}/like`, {
      method: 'DELETE',
    }),
  reportListened: (trackId: string, positionSec: number, completed: boolean) =>
    api<void>('/events/track-listened', {
      method: 'POST',
      body: { trackId, positionSec: Math.round(positionSec), completed },
    }),
  /** Left before the listen event (≥50 %) — a negative signal for My Wave. */
  reportSkipped: (trackId: string, positionSec: number) =>
    api<void>('/events/track-skipped', {
      method: 'POST',
      body: { trackId, positionSec: Math.round(positionSec) },
    }),
  getPosition: (trackId: string) => api<PlaybackPosition>(`/tracks/${encodeURIComponent(trackId)}/position`),
  savePosition: (trackId: string, positionSec: number, keepalive = false) =>
    api<void>(`/tracks/${encodeURIComponent(trackId)}/position`, {
      method: 'PUT',
      body: { positionSec: Math.round(positionSec * 10) / 10 },
      keepalive,
    }),
};

export const playlistsApi = {
  list: () => api<Playlist[]>('/playlists'),
  create: (title: string) => api<Playlist>('/playlists', { method: 'POST', body: { title } }),
  rename: (id: string, title: string) =>
    api<Playlist>(`/playlists/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      body: { title },
    }),
  remove: (id: string) => api<void>(`/playlists/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  get: (id: string) => api<PlaylistWithTracks>(`/playlists/${encodeURIComponent(id)}`).then(playlistWithMedia),
  addTrack: (id: string, trackId: string) =>
    api<void>(`/playlists/${encodeURIComponent(id)}/tracks`, {
      method: 'POST',
      body: { trackId },
    }),
  removeTrack: (id: string, trackId: string) =>
    api<void>(`/playlists/${encodeURIComponent(id)}/tracks/${encodeURIComponent(trackId)}`, {
      method: 'DELETE',
    }),
};

export const waveApi = {
  start: (mode: WaveMode = 'default') =>
    api<WaveSession>('/wave/sessions', { method: 'POST', body: { mode } }).then(waveWithMedia),
  more: (sessionId: string, afterTrackId: string) =>
    api<Track[]>(`/wave/sessions/${encodeURIComponent(sessionId)}/tracks`, {
      query: { after: afterTrackId },
    }).then((ts) => ts.map(withMedia)),
  feedback: (sessionId: string, trackId: string, action: WaveFeedbackAction) =>
    api<void>(`/wave/sessions/${encodeURIComponent(sessionId)}/feedback`, {
      method: 'POST',
      body: { trackId, action },
    }),
};

/** Unified catalog: library + MusicBrainz, every item playable/likeable (ADR 0011). */
export const catalogApi = {
  search: (q: string, signal?: AbortSignal) =>
    api<SearchResult>('/search', { query: { q }, signal }).then(searchWithMedia),
  album: (id: string, signal?: AbortSignal) =>
    api<AlbumView>(`/albums/${encodeURIComponent(id)}`, { signal }).then(albumViewWithMedia),
  artist: (id: string, signal?: AbortSignal) =>
    api<ArtistView>(`/artists/${encodeURIComponent(id)}`, { signal }).then(artistViewWithMedia),
};
