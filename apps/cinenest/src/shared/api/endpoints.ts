import { api } from './client';
import type {
  AuthTokens,
  ContinueItem,
  Page,
  StreamSession,
  Title,
  TitleKind,
  TitleSummary,
  User,
  WatchPosition,
} from './types';

export const PAGE_SIZE = 24;

export const authApi = {
  login: (initData: string) => api<AuthTokens>('/auth/telegram', { method: 'POST', body: { initData }, auth: false }),
  refresh: (refreshToken: string) =>
    api<AuthTokens>('/auth/refresh', { method: 'POST', body: { refreshToken }, auth: false }),
  logout: (refreshToken: string) => api<void>('/auth/logout', { method: 'POST', body: { refreshToken }, auth: false }),
  me: (signal?: AbortSignal) => api<User>('/me', { signal }),
};

const enc = encodeURIComponent;

export interface CatalogFilter {
  q: string;
  kind: TitleKind | null;
}

export const cinemaApi = {
  titles: (f: CatalogFilter, cursor: string | null, signal?: AbortSignal) =>
    api<Page<TitleSummary>>('/cinema/titles', {
      query: { q: f.q.trim(), kind: f.kind, cursor, limit: PAGE_SIZE },
      signal,
    }),
  title: (id: string, signal?: AbortSignal) => api<Title>(`/cinema/titles/${enc(id)}`, { signal }),
  continueWatching: (signal?: AbortSignal) =>
    api<Page<ContinueItem>>('/cinema/continue', { query: { limit: 12 }, signal }),
  watchlist: (cursor: string | null, signal?: AbortSignal) =>
    api<Page<TitleSummary>>('/cinema/watchlist', { query: { cursor, limit: PAGE_SIZE }, signal }),
  setWatchlist: (id: string, on: boolean) =>
    api<void>(`/cinema/titles/${enc(id)}/watchlist`, { method: on ? 'PUT' : 'DELETE' }),
  getPosition: (titleId: string, fileId: string, signal?: AbortSignal) =>
    api<WatchPosition>(`/cinema/titles/${enc(titleId)}/position`, { query: { fileId }, signal }),
  savePosition: (titleId: string, fileId: string, positionSec: number, durationSec: number, keepalive = false) =>
    api<void>(`/cinema/titles/${enc(titleId)}/position`, {
      method: 'PUT',
      body: { fileId, positionSec: Math.round(positionSec), durationSec: Math.round(durationSec) },
      keepalive,
    }),
  startStream: (titleId: string, fileId: string) =>
    api<StreamSession>('/cinema/streams', { method: 'POST', body: { titleId, fileId } }),
  stream: (id: string, signal?: AbortSignal) => api<StreamSession>(`/cinema/streams/${enc(id)}`, { signal }),
  stopStream: (id: string) => api<void>(`/cinema/streams/${enc(id)}`, { method: 'DELETE', keepalive: true }),
};
