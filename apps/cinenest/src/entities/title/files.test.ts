import { describe, expect, it } from 'vitest';
import type { MediaFile } from '@/shared/api/types';
import { groupBySeason, nextEpisode, pickDefaultFile, progressPct, resumeAt, sortByQuality } from './files';

const f = (
  id: string,
  quality: string,
  season: number | null = null,
  episode: number | null = null,
  sizeBytes = 1,
): MediaFile => ({
  id,
  name: id,
  season,
  episode,
  quality,
  sizeBytes,
  durationSec: 60,
});

const series = [
  f('s2e1-720', '720p', 2, 1),
  f('s1e2-1080', '1080p', 1, 2),
  f('s1e1-720', '720p', 1, 1),
  f('s1e1-1080', '1080p', 1, 1),
  f('s1e2-720', '720p', 1, 2),
  f('s2e1-1080', '1080p', 2, 1),
];

describe('groupBySeason', () => {
  it('groups by season ascending, episodes ascending, best quality first', () => {
    const g = groupBySeason(series);
    expect(g.map((s) => s.season)).toEqual([1, 2]);
    expect(g[0]!.files.map((x) => x.id)).toEqual(['s1e1-1080', 's1e1-720', 's1e2-1080', 's1e2-720']);
  });
  it('puts files without a season into season 0', () => {
    expect(groupBySeason([f('m', '720p')])[0]!.season).toBe(0);
  });
});

describe('sortByQuality', () => {
  it('orders 2160p > 1080p > 720p > unknown, smaller first on ties', () => {
    const out = sortByQuality([
      f('a', '720p'),
      f('b', 'cam'),
      f('c', '2160p'),
      f('d', '1080p', null, null, 9),
      f('e', '1080p', null, null, 3),
    ]);
    expect(out.map((x) => x.id)).toEqual(['c', 'e', 'd', 'a', 'b']);
  });
});

describe('pickDefaultFile', () => {
  it('returns null for no files', () => expect(pickDefaultFile([])).toBeNull());
  it('prefers the last watched file', () => expect(pickDefaultFile(series, 's2e1-720')?.id).toBe('s2e1-720'));
  it('ignores an unknown last file id and picks S1E1 1080p', () =>
    expect(pickDefaultFile(series, 'nope')?.id).toBe('s1e1-1080'));
  it('movie without 1080p → best quality', () =>
    expect(pickDefaultFile([f('a', '720p'), f('b', '2160p')])?.id).toBe('b'));
});

describe('nextEpisode', () => {
  it('next episode in the same season, same quality', () => {
    expect(nextEpisode(series, series[2]!)?.id).toBe('s1e2-720');
  });
  it('rolls over to the next season', () => {
    expect(nextEpisode(series, series[1]!)?.id).toBe('s2e1-1080');
  });
  it('falls back to another quality when the same one is missing', () => {
    expect(nextEpisode([f('a', '720p', 1, 1), f('b', '1080p', 1, 2)], f('a', '720p', 1, 1))?.id).toBe('b');
  });
  it('null for the last episode and for movies', () => {
    expect(nextEpisode(series, series[5]!)).toBeNull();
    expect(nextEpisode([f('m', '720p')], f('m', '720p'))).toBeNull();
  });
});

describe('resumeAt / progressPct', () => {
  it.each([
    [null, 0],
    [{ positionSec: 5, durationSec: 100 }, 0],
    [{ positionSec: 99, durationSec: 100 }, 0],
    [{ positionSec: 60, durationSec: 100 }, 55],
    [{ positionSec: 60, durationSec: 0 }, 55],
    [{ positionSec: Number.NaN, durationSec: 100 }, 0],
  ])('resumeAt(%o) = %d', (pos, want) => expect(resumeAt(pos)).toBe(want));
  it('progressPct clamps and handles zero duration', () => {
    expect(progressPct({ positionSec: 30, durationSec: 60 })).toBe(50);
    expect(progressPct({ positionSec: 90, durationSec: 60 })).toBe(100);
    expect(progressPct({ positionSec: 10, durationSec: 0 })).toBe(0);
  });
});
