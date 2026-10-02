/** Contract types — see docs/api/waveplayer-contract.md (source of truth). */
export interface User {
  /** UUID v4 (spec §5.2) */
  id: string;
  telegramId: number;
  firstName: string;
  lastName: string | null;
  username: string | null;
  photoUrl: string | null;
  languageCode: string | null;
  /** RBAC role (api-gateway). */
  role?: 'user' | 'admin';
}

export interface AuthTokens {
  accessToken: string;
  refreshToken: string;
  /** Access-token lifetime in seconds (api-gateway adds it; optional in the contract). */
  expiresIn?: number;
  user: User;
}

export interface Track {
  id: string;
  title: string;
  artist: string;
  album: string | null;
  coverUrl: string | null;
  durationSec: number;
  liked: boolean;
  /** Wave only: why this track was picked (explainability, ADR 0010). */
  reason?: WaveReason | null;
  /** Not on the server yet: rendered like any track, playing/liking acquires it (ADR 0011). */
  remote?: boolean;
}

/** Album of the unified catalog (library + MusicBrainz, rendered identically). */
export interface Album {
  id: string;
  title: string;
  artist: string;
  artistId: string | null;
  year: number | null;
  coverUrl: string | null;
}

export interface Artist {
  id: string;
  name: string;
  coverUrl: string | null;
}

/** GET /search — unified results. */
export interface SearchResult {
  tracks: Track[];
  albums: Album[];
  artists: Artist[];
}

export interface AlbumView {
  album: Album;
  tracks: Track[];
}

export interface ArtistView {
  artist: Artist;
  albums: Album[];
  tracks: Track[];
}

/** 202 from stream-url: the track is being fetched, ask again after retryAfterMs. */
export interface StreamPending {
  state: string;
  progress: number;
  retryAfterMs: number;
}

export type WaveReasonKind =
  | 'because_you_liked'
  | 'artist_you_like'
  | 'genre_you_like'
  | 'mood_you_like'
  | 'similar_listeners'
  | 'popular'
  | 'new_in_catalog'
  | 'discovery'
  | 'favorite'
  | 'session_artist';

export interface WaveReason {
  kind: WaveReasonKind | (string & {});
  refTrackId?: string;
  refTitle?: string;
  refArtist?: string;
  artist?: string;
  genre?: string;
  tag?: string;
}

export const WAVE_MODES = ['default', 'calm', 'energetic', 'discover', 'favorites'] as const;
export type WaveMode = (typeof WAVE_MODES)[number];

/** Cursor pagination (never offset). */
export interface Page<T> {
  items: T[];
  nextCursor: string | null;
}

export interface Playlist {
  id: string;
  title: string;
  trackCount: number;
  createdAt: string;
}

export interface PlaylistWithTracks {
  playlist: Playlist;
  tracks: Track[];
}

export interface WaveSession {
  sessionId: string;
  tracks: Track[];
  /** 'reco' = reco-service, 'fallback' = local heuristic (reco down/slow). */
  strategy?: 'reco' | 'fallback';
  mode?: WaveMode;
}

export interface StreamUrl {
  url: string;
  expiresAt: string;
}

export interface PlaybackPosition {
  trackId: string;
  positionSec: number;
  updatedAt: string;
}

export type WaveFeedbackAction = 'like' | 'skip';

export interface ApiErrorBody {
  message?: string;
  code?: string;
  service?: string;
}
