import { config } from '../config';
import type {
  Album,
  AlbumView,
  Artist,
  ArtistView,
  Page,
  PlaylistWithTracks,
  SearchResult,
  StreamUrl,
  Track,
  WaveSession,
} from './types';

/**
 * music-service returns signed media links relative to the gateway
 * (`/api/v1/stream/...`). With the default same-origin setup they work as-is;
 * when VITE_API_URL points at another origin they must be resolved against it,
 * because <audio>/<img> would otherwise hit the mini app host.
 */
export function mediaUrl<T extends string | null>(url: T, base: string = config.apiUrl): T {
  if (!url || !base || !url.startsWith('/') || url.startsWith('//')) return url;
  return `${base}${url}` as T;
}

export const withMedia = (t: Track): Track => ({ ...t, coverUrl: mediaUrl(t.coverUrl) });
export const pageWithMedia = (p: Page<Track>): Page<Track> => ({ ...p, items: p.items.map(withMedia) });
export const playlistWithMedia = (p: PlaylistWithTracks): PlaylistWithTracks => ({
  ...p,
  tracks: p.tracks.map(withMedia),
});
export const waveWithMedia = (w: WaveSession): WaveSession => ({ ...w, tracks: w.tracks.map(withMedia) });
export const streamWithMedia = (s: StreamUrl): StreamUrl => ({ ...s, url: mediaUrl(s.url) });
export const albumWithMedia = (a: Album): Album => ({ ...a, coverUrl: mediaUrl(a.coverUrl) });
export const artistWithMedia = (a: Artist): Artist => ({ ...a, coverUrl: mediaUrl(a.coverUrl) });
export const searchWithMedia = (r: SearchResult): SearchResult => ({
  tracks: (r.tracks ?? []).map(withMedia),
  albums: (r.albums ?? []).map(albumWithMedia),
  artists: (r.artists ?? []).map(artistWithMedia),
});
export const albumViewWithMedia = (v: AlbumView): AlbumView => ({
  album: albumWithMedia(v.album),
  tracks: (v.tracks ?? []).map(withMedia),
});
export const artistViewWithMedia = (v: ArtistView): ArtistView => ({
  artist: artistWithMedia(v.artist),
  albums: (v.albums ?? []).map(albumWithMedia),
  tracks: (v.tracks ?? []).map(withMedia),
});
