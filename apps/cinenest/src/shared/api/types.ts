/** Contract types — docs/api/cinema.openapi.yaml (+ gateway.openapi.yaml for auth). */
export interface User {
  /** UUID v4 (spec §5.2) */
  id: string;
  telegramId: number;
  firstName: string;
  lastName: string | null;
  username: string | null;
  photoUrl: string | null;
  languageCode: string | null;
  role?: 'user' | 'admin';
}

export interface AuthTokens {
  accessToken: string;
  refreshToken: string;
  expiresIn?: number;
  user: User;
}

export interface ApiErrorBody {
  message?: string;
  code?: string;
  service?: string;
}

export interface Page<T> {
  items: T[];
  nextCursor: string | null;
}

export type TitleKind = 'movie' | 'series';

export interface TitleSummary {
  id: string;
  kind: TitleKind;
  title: string;
  originalTitle: string | null;
  year: number | null;
  posterUrl: string | null;
  rating: number | null;
  genres: string[];
  inWatchlist: boolean;
}

export interface MediaFile {
  id: string;
  name: string;
  season: number | null;
  episode: number | null;
  quality: string;
  sizeBytes: number;
  durationSec: number | null;
}

export interface Title extends TitleSummary {
  description: string;
  backdropUrl: string | null;
  runtimeMin: number | null;
  files: MediaFile[];
}

export type StreamStatus = 'warming' | 'ready' | 'failed';

export interface StreamSession {
  id: string;
  titleId: string;
  fileId: string;
  status: StreamStatus;
  /** TorrServer preload progress, 0..100 */
  bufferedPct: number;
  peers: number;
  speedBps: number;
  /** Absolute path on our domain; null until ready. */
  hlsUrl: string | null;
  error: string | null;
}

export interface WatchPosition {
  titleId: string;
  fileId: string;
  positionSec: number;
  durationSec: number;
  updatedAt: string;
}

export interface ContinueItem {
  title: TitleSummary;
  file: MediaFile;
  position: WatchPosition;
}
