export type HealthStatus = 'ok' | 'down' | 'off';

export interface HealthService {
  name: string;
  status: HealthStatus;
}

export interface AdminUser {
  id: string;
  telegramId: number;
  firstName: string;
  lastName: string | null;
  username: string | null;
  languageCode: string | null;
  role: string;
  createdAt: string;
  lastLoginAt: string | null;
}

export interface AdminTrack {
  id: string;
  title: string;
  artist: string;
  album?: string;
  at: string;
}

export interface TasteCount {
  name: string;
  count: number;
}

export interface UserMusic {
  userId: string;
  likes: AdminTrack[];
  recent: AdminTrack[];
  artists: TasteCount[];
  genres: TasteCount[];
}

export type DownloadStatus = 'queued' | 'running' | 'done' | 'failed';

export interface AdminDownload {
  id: string;
  userId: string;
  url: string;
  source: string;
  status: DownloadStatus;
  stage?: string;
  title?: string;
  attempts: number;
  errorKind?: string;
  errorMessage?: string;
  createdAt: string;
  finishedAt?: string;
}

export interface CatalogHealth {
  artists: number;
  albums: number;
  tracks: number;
  deletedTracks: number;
  youtubeTracks: number;
  libraryTracks: number;
  lastSyncedAt: string | null;
}

export interface SearchTrack {
  id: string;
  title: string;
  artist: string;
  album?: string | null;
  durationSec: number;
}
