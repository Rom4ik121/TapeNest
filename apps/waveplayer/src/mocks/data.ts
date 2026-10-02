import type { Album, Artist, Playlist, Track } from '@/shared/api/types';

/**
 * Mock catalog + mutable mock DB. NOTE the declaration order: `mockDb` is
 * declared before anything that reads it (the old code hit a TDZ error), and
 * `liked` is computed at response time, never at module init.
 */
export interface MockPlaylist {
  id: string;
  title: string;
  trackIds: string[];
  createdAt: string;
}

export const mockDb = {
  liked: new Set<string>(['t3', 't8', 't15', 't22']),
  playlists: [] as MockPlaylist[],
  positions: new Map<string, { positionSec: number; updatedAt: string }>(),
  /** Remote mock tracks already "fetched" (stream-url answers 200). */
  acquired: new Set<string>(),
};

const PALETTE = ['#E7E4DE', '#EEAA11', '#4FB3B3', '#BB3381', '#3F1D50', '#16141C'] as const;
const GRADIENTS: ReadonlyArray<readonly string[]> = [
  ['#E7E4DE', '#EEAA11'],
  ['#EEAA11', '#BB3381'],
  ['#4FB3B3', '#BB3381'],
  ['#EEAA11', '#BB3381', '#3F1D50', '#16141C'],
  ['#BB3381', '#3F1D50'],
  ['#4FB3B3', '#3F1D50'],
];

function hash(s: string): number {
  let h = 0;
  for (const c of s) h = (Math.imul(h, 31) + c.charCodeAt(0)) | 0;
  return Math.abs(h);
}

/** Deterministic palette-based SVG cover (offline, no external requests). */
export function svgCover(id: string): string {
  const h = hash(id);
  const g = GRADIENTS[h % GRADIENTS.length] ?? GRADIENTS[0]!;
  const stops = g
    .map((c, i) => `<stop offset="${g.length === 1 ? 0 : i / (g.length - 1)}" stop-color="${c}"/>`)
    .join('');
  const accent = PALETTE[(h >> 3) % PALETTE.length];
  const variant = (h >> 5) % 4;
  const INK = '#16141C';
  const CREAM = '#E7E4DE';
  const shapes = [
    // sunset: disc cut by horizontal stripes
    `<circle cx="100" cy="92" r="54" fill="${accent}" opacity=".85"/>` +
      [0, 1, 2, 3]
        .map((i) => `<rect x="30" y="${104 + i * 11}" width="140" height="${3 + i}" fill="${INK}" opacity=".35"/>`)
        .join(''),
    // layered waves
    `<path d="M0 118 Q50 84 100 118 T200 118 V200 H0Z" fill="${INK}" opacity=".22"/>` +
      `<path d="M0 142 Q50 110 100 142 T200 142 V200 H0Z" fill="${accent}" opacity=".55"/>` +
      `<path d="M0 166 Q50 138 100 166 T200 166" stroke="${CREAM}" stroke-opacity=".6" stroke-width="3" fill="none"/>`,
    // vinyl
    `<circle cx="100" cy="100" r="66" fill="${INK}" opacity=".78"/>` +
      [58, 50, 42, 34]
        .map((r) => `<circle cx="100" cy="100" r="${r}" fill="none" stroke="${CREAM}" stroke-opacity=".12"/>`)
        .join('') +
      `<circle cx="100" cy="100" r="20" fill="${accent}"/><circle cx="100" cy="100" r="3" fill="${INK}"/>`,
    // equalizer bars
    [0, 1, 2, 3, 4, 5, 6]
      .map((i) => {
        const bh = 30 + ((h >> (i + 2)) % 90);
        return `<rect x="${33 + i * 20}" y="${160 - bh}" width="12" height="${bh}" rx="6" fill="${i % 2 ? CREAM : accent}" opacity="${i % 2 ? 0.55 : 0.85}"/>`;
      })
      .join(''),
  ][variant];
  const svg =
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 200">` +
    `<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">${stops}</linearGradient></defs>` +
    `<rect width="200" height="200" fill="url(#g)"/>${shapes ?? ''}</svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

const RAW: Array<[string, string, string]> = [
  ['Neon Drive', 'Volt Kitten', 'Midnight Circuit'],
  ['Компилятор', 'Дебаг', 'Runtime'],
  ['Aurora Skies', 'Lumen Field', 'Northlight'],
  ['Тихий вечер', 'Мята', 'Дождь по стеклу'],
  ['Hyperloop', 'Cassette Runners', 'Overclock'],
  ['Волна', 'Прибой', 'Летняя ночь'],
  ['Coffee Break', 'Lo-Fi Society', 'Daylight'],
  ['Midnight Circuit', 'Volt Kitten', 'Midnight Circuit'],
  ['Северный ветер', 'Айсберг', 'Northlight'],
  ['Pixel Sunset', 'Neon Arcade', 'Overclock'],
  ['Дождь по стеклу', 'Мята', 'Дождь по стеклу'],
  ['Overclock', 'Cassette Runners', 'Overclock'],
  ['Летняя ночь', 'Прибой', 'Летняя ночь'],
  ['Slow Motion', 'Lumen Field', 'Northlight'],
  ['Amber Hour', 'Tape Nest Trio', 'Gradients'],
  ['Magenta Sky', 'Tape Nest Trio', 'Gradients'],
  ['Deep Purple Rain', 'Violet Hours', 'Gradients'],
  ['Teal Lagoon', 'Coastline', 'Blue Hour'],
  ['Кассета', 'Магнитофон', 'Плёнка'],
  ['Late Train', 'Night Commute', 'Stations'],
  ['Городские огни', 'Неон', 'Проспект'],
  ['Satellite', 'Orbit Club', 'Zero G'],
  ['Бархат', 'Шёлк', 'Ткани'],
  ['Golden Static', 'Radio Bloom', 'FM Dreams'],
  ['Paper Planes', 'Lo-Fi Society', 'Daylight'],
  ['Февраль', 'Айсберг', 'Зима'],
  ['Moonwalk', 'Orbit Club', 'Zero G'],
  ['Soft Focus', 'Violet Hours', 'Polaroid'],
  ['Маяк', 'Прибой', 'Берег'],
  ['Sunday Loop', 'Radio Bloom', 'FM Dreams'],
  ['Glass Garden', 'Lumen Field', 'Greenhouse'],
  ['Туман', 'Мята', 'Утро'],
  ['Arcade Heart', 'Neon Arcade', 'Continue?'],
  ['Slow Dance', 'Violet Hours', 'Polaroid'],
  ['Звёздный путь', 'Неон', 'Проспект'],
  ['Night Swim', 'Coastline', 'Blue Hour'],
  ['Cloud Nine', 'Tape Nest Trio', 'Gradients'],
  ['Метро', 'Дебаг', 'Runtime'],
  ['Violet Noise', 'Volt Kitten', 'Afterglow'],
  ['Afterglow', 'Volt Kitten', 'Afterglow'],
  ['Сумерки', 'Шёлк', 'Ткани'],
  ['Rewind', 'Cassette Runners', 'B-Sides'],
  ['Honey Tape', 'Radio Bloom', 'B-Sides'],
  ['Полночь', 'Магнитофон', 'Плёнка'],
  ['Lighthouse', 'Coastline', 'Blue Hour'],
  ['Starlight Avenue', 'Night Commute', 'Stations'],
  ['Эхо', 'Айсберг', 'Зима'],
  ['Last Dance', 'Orbit Club', 'Zero G'],
];

export const CATALOG: ReadonlyArray<Omit<Track, 'liked'>> = RAW.map(([title, artist, album], i) => {
  const id = `t${i + 1}`;
  return {
    id,
    title,
    artist,
    album,
    coverUrl: svgCover(id),
    durationSec: 140 + (hash(id + title) % 120),
    // every 5th track is "not on the server yet": plays after a short fetch
    ...(i % 5 === 4 ? { remote: true } : {}),
  };
});

const slug = (s: string | null): string => (s ?? '').toLowerCase().replace(/[^\p{L}\p{N}]+/gu, '-');
export const albumId = (album: string | null): string => `al-${slug(album)}`;
export const artistId = (artist: string): string => `ar-${slug(artist)}`;

export function mockAlbums(): Album[] {
  const seen = new Map<string, Album>();
  for (const t of CATALOG) {
    const id = albumId(t.album);
    if (!t.album || seen.has(id)) continue;
    seen.set(id, {
      id,
      title: t.album,
      artist: t.artist,
      artistId: artistId(t.artist),
      year: 2024,
      coverUrl: t.coverUrl,
    });
  }
  return [...seen.values()];
}

export function mockArtists(): Artist[] {
  const seen = new Map<string, Artist>();
  for (const t of CATALOG) {
    const id = artistId(t.artist);
    if (!seen.has(id)) seen.set(id, { id, name: t.artist, coverUrl: t.coverUrl });
  }
  return [...seen.values()];
}

export function findTrack(id: string): Omit<Track, 'liked'> | undefined {
  return CATALOG.find((t) => t.id === id);
}

export function withLiked(t: Omit<Track, 'liked'>): Track {
  return { ...t, liked: mockDb.liked.has(t.id) };
}

export function toPlaylist(p: MockPlaylist): Playlist {
  return {
    id: p.id,
    title: p.title,
    trackCount: p.trackIds.length,
    createdAt: p.createdAt,
  };
}

export function resetMockDb(): void {
  mockDb.acquired = new Set();
  mockDb.liked = new Set(['t3', 't8', 't15', 't22']);
  mockDb.positions.clear();
  mockDb.playlists = [
    {
      id: 'p1',
      title: 'В дорогу',
      trackIds: ['t1', 't5', 't9', 't12', 't20', 't46'],
      createdAt: '2026-06-01T10:00:00Z',
    },
    {
      id: 'p2',
      title: 'Кодинг',
      trackIds: ['t2', 't7', 't25', 't38'],
      createdAt: '2026-06-12T10:00:00Z',
    },
    {
      id: 'p3',
      title: 'Спокойное',
      trackIds: ['t4', 't11', 't14', 't28', 't32'],
      createdAt: '2026-07-03T10:00:00Z',
    },
  ];
}
resetMockDb();
