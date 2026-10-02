import type { MediaFile, Title, TitleKind, WatchPosition } from '@/shared/api/types';
import { uuidFromSeed } from './uuid';

/** Fictional dev catalog (no real titles) with offline SVG artwork on the TapeNest palette. */
const PALETTE = ['#E7E4DE', '#EEAA11', '#4FB3B3', '#BB3381', '#3F1D50', '#16141C'] as const;
const GRADIENTS: ReadonlyArray<readonly string[]> = [
  ['#EEAA11', '#BB3381'],
  ['#4FB3B3', '#BB3381'],
  ['#EEAA11', '#BB3381', '#3F1D50', '#16141C'],
  ['#BB3381', '#3F1D50'],
  ['#4FB3B3', '#3F1D50'],
  ['#E7E4DE', '#EEAA11'],
];

function hash(s: string): number {
  let h = 0;
  for (const c of s) h = (Math.imul(h, 31) + c.charCodeAt(0)) | 0;
  return Math.abs(h);
}

const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

function gradientDefs(id: string, h: number): string {
  const g = GRADIENTS[h % GRADIENTS.length] ?? GRADIENTS[0]!;
  const stops = g.map((c, i) => `<stop offset="${i / Math.max(1, g.length - 1)}" stop-color="${c}"/>`).join('');
  return `<defs><linearGradient id="${id}" x1="0" y1="0" x2="1" y2="1">${stops}</linearGradient></defs>`;
}

function art(h: number, w: number, hgt: number): string {
  const accent = PALETTE[(h >> 3) % 4];
  const cx = w * (0.3 + ((h >> 4) % 40) / 100);
  switch ((h >> 6) % 3) {
    case 0: // sun over horizon
      return (
        `<circle cx="${cx}" cy="${hgt * 0.42}" r="${w * 0.24}" fill="${accent}" opacity=".85"/>` +
        `<path d="M0 ${hgt * 0.55} Q${w * 0.3} ${hgt * 0.47} ${w * 0.55} ${hgt * 0.56} T${w} ${hgt * 0.52} V${hgt} H0Z" fill="#16141C" opacity=".55"/>`
      );
    case 1: // mountains
      return (
        `<path d="M0 ${hgt * 0.7} L${w * 0.3} ${hgt * 0.35} L${w * 0.55} ${hgt * 0.6} L${w * 0.75} ${hgt * 0.4} L${w} ${hgt * 0.65} V${hgt} H0Z" fill="#16141C" opacity=".5"/>` +
        `<circle cx="${w * 0.78}" cy="${hgt * 0.2}" r="${w * 0.07}" fill="#E7E4DE" opacity=".8"/>`
      );
    default: // city skyline
      return (
        Array.from({ length: 8 }, (_, i) => {
          const bh = hgt * (0.2 + ((h >> (i + 1)) % 30) / 100);
          return `<rect x="${(i * w) / 8}" y="${hgt * 0.72 - bh}" width="${w / 8 - 4}" height="${bh + hgt * 0.3}" fill="#16141C" opacity="${0.35 + (i % 3) * 0.12}"/>`;
        }).join('') + `<circle cx="${cx}" cy="${hgt * 0.25}" r="${w * 0.1}" fill="${accent}" opacity=".7"/>`
      );
  }
}

export function posterSvg(id: string, title: string): string {
  const h = hash(id);
  const words = title.split(' ');
  const lines: string[] = [];
  for (const w of words) {
    const last = lines[lines.length - 1];
    if (last && (last + ' ' + w).length <= 12) lines[lines.length - 1] = `${last} ${w}`;
    else lines.push(w);
  }
  const text = lines
    .slice(0, 3)
    .map((l, i) => `<tspan x="20" dy="${i === 0 ? 0 : 30}">${esc(l.toUpperCase())}</tspan>`)
    .join('');
  const svg =
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 200 300">${gradientDefs('g', h)}` +
    `<rect width="200" height="300" fill="url(#g)"/>${art(h, 200, 300)}` +
    `<rect y="200" width="200" height="100" fill="#16141C" opacity=".35"/>` +
    `<text x="20" y="${262 - (Math.min(lines.length, 3) - 1) * 30}" font-family="system-ui,sans-serif" font-size="26" font-weight="800" fill="#E7E4DE" letter-spacing="1">${text}</text></svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

export function backdropSvg(id: string): string {
  const h = hash(id) + 7;
  const svg =
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 225">${gradientDefs('b', h)}` +
    `<rect width="400" height="225" fill="url(#b)"/>${art(h, 400, 225)}</svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}

type Raw = [
  title: string,
  original: string | null,
  kind: TitleKind,
  year: number,
  rating: number,
  genres: string[],
  min: number,
];
const RAW: Raw[] = [
  ['Янтарный горизонт', 'Amber Horizon', 'movie', 2024, 7.8, ['drama', 'adventure'], 128],
  ['Тихая гавань', 'Quiet Harbor', 'series', 2023, 8.4, ['drama', 'mystery'], 48],
  ['Неоновый дождь', 'Neon Rain', 'movie', 2022, 7.1, ['scifi', 'thriller'], 117],
  ['Последний маяк', 'The Last Lighthouse', 'movie', 2021, 6.9, ['drama'], 104],
  ['Станция Бирюза', 'Turquoise Station', 'series', 2024, 8.1, ['scifi'], 52],
  ['Лисья тропа', 'Fox Trail', 'movie', 2020, 7.4, ['family', 'adventure'], 96],
  ['Код полуночи', 'Midnight Code', 'series', 2022, 7.9, ['thriller', 'crime'], 45],
  ['Бумажные крылья', 'Paper Wings', 'movie', 2019, 7.2, ['comedy', 'drama'], 101],
  ['Северный экспресс', 'Northern Express', 'movie', 2023, 6.8, ['action'], 112],
  ['Сад камней', 'Stone Garden', 'series', 2021, 8.7, ['drama'], 58],
  ['Пурпурный рассвет', 'Purple Dawn', 'movie', 2024, 7.0, ['fantasy'], 133],
  ['Город шёпотов', 'City of Whispers', 'series', 2020, 7.6, ['mystery', 'crime'], 50],
  ['Двойной узел', 'Double Knot', 'movie', 2018, 6.5, ['comedy'], 94],
  ['Глубина', 'The Depth', 'movie', 2022, 7.7, ['thriller', 'scifi'], 109],
  ['Кассетное лето', 'Cassette Summer', 'series', 2023, 8.0, ['comedy', 'drama'], 32],
  ['Мост через туман', 'Bridge Through Fog', 'movie', 2021, 7.3, ['drama', 'romance'], 118],
  ['Ржавые звёзды', 'Rusty Stars', 'movie', 2020, 6.7, ['scifi', 'action'], 125],
  ['Дом на холме', 'House on the Hill', 'series', 2024, 7.5, ['horror', 'mystery'], 44],
  ['Ветер перемен', 'Wind of Change', 'movie', 2017, 7.9, ['drama'], 140],
  ['Маленький оркестр', 'Little Orchestra', 'movie', 2023, 8.2, ['family', 'music'], 99],
  ['Архив 47', 'Archive 47', 'series', 2022, 7.2, ['thriller'], 47],
  ['Солёный ветер', 'Salt Wind', 'movie', 2019, 6.9, ['adventure'], 107],
  ['Полярная ночь', 'Polar Night', 'movie', 2024, 7.6, ['thriller', 'drama'], 121],
  ['Шахматист', 'The Chess Player', 'series', 2021, 8.5, ['drama'], 55],
  ['Облачный атлас снов', 'Cloud Atlas of Dreams', 'movie', 2022, 7.0, ['fantasy', 'drama'], 136],
  ['Огни порта', 'Harbor Lights', 'movie', 2020, 6.6, ['romance'], 98],
  ['Эхо', 'Echo', 'series', 2023, 7.8, ['scifi', 'mystery'], 49],
  ['Медленный вальс', 'Slow Waltz', 'movie', 2018, 7.4, ['romance', 'music'], 102],
];

const GB = 1024 ** 3;
function filesFor(id: string, kind: TitleKind, min: number, name: string): MediaFile[] {
  const slug = name.replace(/\s+/g, '.');
  if (kind === 'movie') {
    return (
      [
        ['720p', 1.4],
        ['1080p', 3.2],
        ['2160p', 14.8],
      ] as const
    ).map(([quality, gb]) => ({
      id: uuidFromSeed(`${id}:${quality}`),
      name: `${slug}.${quality}.mkv`,
      season: null,
      episode: null,
      quality,
      sizeBytes: Math.round(gb * GB * (min / 120)),
      durationSec: min * 60,
    }));
  }
  const out: MediaFile[] = [];
  for (let s = 1; s <= 2; s++) {
    for (let e = 1; e <= 6; e++) {
      for (const [quality, gb] of [
        ['720p', 0.6],
        ['1080p', 1.3],
      ] as const) {
        out.push({
          id: uuidFromSeed(`${id}:s${s}e${e}:${quality}`),
          name: `${slug}.S0${s}E0${e}.${quality}.mkv`,
          season: s,
          episode: e,
          quality,
          sizeBytes: Math.round(gb * GB * (min / 50)),
          durationSec: min * 60,
        });
      }
    }
  }
  return out;
}

export type MockTitle = Omit<Title, 'inWatchlist'>;

export const TITLES: MockTitle[] = RAW.map(([title, original, kind, year, rating, genres, min], i) => {
  const id = uuidFromSeed(`title:${i}`);
  return {
    id,
    kind,
    title,
    originalTitle: original,
    year,
    posterUrl: posterSvg(id, original ?? title),
    rating,
    genres,
    description:
      'Демо-описание: каталог CineNest наполняется streaming-service на этапе 4. ' +
      'Здесь будет синопсис из карточки раздачи. Demo synopsis — real data arrives in stage 4.',
    backdropUrl: backdropSvg(id),
    runtimeMin: min,
    files: filesFor(id, kind, min, original ?? title),
  };
});

export function findTitle(id: string): MockTitle | undefined {
  return TITLES.find((t) => t.id === id);
}

export interface MockDb {
  watchlist: Set<string>;
  /** key `${titleId}:${fileId}` */
  positions: Map<string, WatchPosition>;
}

function initialDb(): MockDb {
  const t1 = TITLES[1]!;
  const f1 = t1.files.find((f) => f.season === 1 && f.episode === 3 && f.quality === '1080p')!;
  const t0 = TITLES[0]!;
  const f0 = t0.files.find((f) => f.quality === '1080p')!;
  const now = Date.now();
  return {
    watchlist: new Set([TITLES[4]!.id, TITLES[9]!.id, TITLES[13]!.id]),
    positions: new Map([
      [
        `${t1.id}:${f1.id}`,
        {
          titleId: t1.id,
          fileId: f1.id,
          positionSec: 1260,
          durationSec: 2880,
          updatedAt: new Date(now - 3600e3).toISOString(),
        },
      ],
      [
        `${t0.id}:${f0.id}`,
        {
          titleId: t0.id,
          fileId: f0.id,
          positionSec: 22,
          durationSec: 60,
          updatedAt: new Date(now - 86400e3).toISOString(),
        },
      ],
    ]),
  };
}

export let mockDb: MockDb = initialDb();
export function resetMockDb(): void {
  mockDb = initialDb();
}
