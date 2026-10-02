import type { TFunction } from 'i18next';
import type { Track } from '@/shared/api/types';

/** Human caption for a wave pick ("Because you liked …"); null when unknown. */
export function reasonText(t: TFunction, track: Pick<Track, 'artist' | 'reason'>): string | null {
  const r = track.reason;
  if (!r?.kind) return null;
  const key = `wave.reason.${r.kind}`;
  switch (r.kind) {
    case 'because_you_liked':
      return r.refTitle ? t(key, { title: r.refTitle }) : t('wave.reason.discovery');
    case 'artist_you_like':
    case 'session_artist':
      return t(key, { artist: r.artist || track.artist });
    case 'genre_you_like':
      return r.genre ? t(key, { genre: r.genre }) : null;
    case 'mood_you_like':
      return r.tag ? t(key, { tag: t(`wave.tags.${r.tag}`, { defaultValue: r.tag }) }) : null;
    case 'similar_listeners':
    case 'popular':
    case 'new_in_catalog':
    case 'discovery':
    case 'favorite':
      return t(key);
    default:
      return null;
  }
}
