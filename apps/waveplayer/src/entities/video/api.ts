import { api, ApiError } from '@/shared/api/client';
import type { FileLink, VideoJob, VideoPage } from './types';

export const downloadsApi = {
  list: (cursor: string | null, signal?: AbortSignal) =>
    api<VideoPage>('/downloads', { query: { cursor, limit: 30 }, signal }),
  get: (id: string, signal?: AbortSignal) => api<VideoJob>(`/downloads/${encodeURIComponent(id)}`, { signal }),
  create: (url: string) => api<VideoJob>('/downloads', { method: 'POST', body: { url } }),
  rename: (id: string, title: string) =>
    api<VideoJob>(`/downloads/${encodeURIComponent(id)}`, { method: 'PATCH', body: { title } }),
  remove: (id: string) => api<void>(`/downloads/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  trim: (id: string, startSec: number, endSec: number) =>
    api<VideoJob>(`/downloads/${encodeURIComponent(id)}/trim`, {
      method: 'POST',
      body: { startSec, endSec },
    }),
  fileUrl: (id: string, signal?: AbortSignal) =>
    api<FileLink>(`/downloads/${encodeURIComponent(id)}/file`, { query: { redirect: 'false' }, signal }),
};

/** i18n key for a download/library error. */
export function downloadErrorKey(error: unknown): string {
  if (!(error instanceof ApiError)) return 'videos.errors.generic';
  switch (error.code) {
    case 'INVALID_URL':
    case 'INVALID':
    case 'FORBIDDEN_HOST':
      return 'videos.errors.invalid';
    case 'UNSUPPORTED_SOURCE':
      return 'videos.errors.unsupported';
    case 'PLAYLIST_NOT_SUPPORTED':
      return 'videos.errors.playlist';
    case 'QUOTA_ACTIVE':
      return 'videos.errors.quotaActive';
    case 'QUOTA_DAILY':
      return 'videos.errors.quotaDaily';
    case 'EDIT_FAILED':
      return 'videos.errors.trim';
    case 'EDIT_UNAVAILABLE':
      return 'videos.errors.trimOff';
    case 'NOT_READY':
      return 'videos.errors.notReady';
    default:
      if (error.status === 0) return 'error.network';
      return 'videos.errors.generic';
  }
}
