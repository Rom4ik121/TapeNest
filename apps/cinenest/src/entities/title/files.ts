import type { MediaFile, WatchPosition } from '@/shared/api/types';

export interface SeasonGroup {
  season: number;
  files: MediaFile[];
}

const QUALITY_RANK: Record<string, number> = { '2160p': 4, '1080p': 3, '720p': 2, '480p': 1 };
const rank = (q: string): number => QUALITY_RANK[q] ?? 0;

/** Series files grouped by season, episodes ascending. Files without a season are season 0. */
export function groupBySeason(files: readonly MediaFile[]): SeasonGroup[] {
  const map = new Map<number, MediaFile[]>();
  for (const f of files) {
    const s = f.season ?? 0;
    const list = map.get(s) ?? [];
    list.push(f);
    map.set(s, list);
  }
  return [...map.entries()]
    .sort(([a], [b]) => a - b)
    .map(([season, list]) => ({
      season,
      files: list.sort((a, b) => (a.episode ?? 0) - (b.episode ?? 0) || rank(b.quality) - rank(a.quality)),
    }));
}

/** Movie versions, best quality first. */
export function sortByQuality(files: readonly MediaFile[]): MediaFile[] {
  return [...files].sort((a, b) => rank(b.quality) - rank(a.quality) || a.sizeBytes - b.sizeBytes);
}

/**
 * Default file for "Watch": the one being watched (continue), otherwise 1080p if
 * present (good quality without huge warm-up), otherwise the best available.
 */
export function pickDefaultFile(files: readonly MediaFile[], lastFileId?: string | null): MediaFile | null {
  if (files.length === 0) return null;
  const last = lastFileId ? files.find((f) => f.id === lastFileId) : undefined;
  if (last) return last;
  const sorted = [...files].sort((a, b) => (a.season ?? 0) - (b.season ?? 0) || (a.episode ?? 0) - (b.episode ?? 0));
  const first = sorted[0];
  const firstEp = sorted.filter((f) => f.season === first?.season && f.episode === first?.episode);
  return firstEp.find((f) => f.quality === '1080p') ?? sortByQuality(firstEp)[0] ?? first ?? null;
}

/** Next episode after `file` (same quality preferred), or null. */
export function nextEpisode(files: readonly MediaFile[], file: MediaFile): MediaFile | null {
  if (file.episode === null) return null;
  const later = files
    .filter(
      (f) =>
        f.episode !== null &&
        ((f.season ?? 0) > (file.season ?? 0) ||
          ((f.season ?? 0) === (file.season ?? 0) && f.episode > (file.episode ?? 0))),
    )
    .sort((a, b) => (a.season ?? 0) - (b.season ?? 0) || (a.episode ?? 0) - (b.episode ?? 0));
  const target = later[0];
  if (!target) return null;
  const same = later.filter((f) => f.season === target.season && f.episode === target.episode);
  return same.find((f) => f.quality === file.quality) ?? same[0] ?? null;
}

/**
 * Where to resume: ignore tiny progress (< 10 s) and finished files (> 95 %),
 * step back 5 s so the viewer regains context.
 */
export function resumeAt(pos: Pick<WatchPosition, 'positionSec' | 'durationSec'> | null | undefined): number {
  if (!pos) return 0;
  const { positionSec, durationSec } = pos;
  if (!Number.isFinite(positionSec) || positionSec < 10) return 0;
  if (durationSec > 0 && positionSec / durationSec > 0.95) return 0;
  return Math.max(0, positionSec - 5);
}

export function progressPct(pos: Pick<WatchPosition, 'positionSec' | 'durationSec'>): number {
  return pos.durationSec > 0 ? Math.min(100, Math.max(0, (pos.positionSec / pos.durationSec) * 100)) : 0;
}
