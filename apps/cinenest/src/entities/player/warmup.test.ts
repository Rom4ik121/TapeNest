import { describe, expect, it } from 'vitest';
import type { StreamSession } from '@/shared/api/types';
import { streamPollInterval, warmupView } from './warmup';

const s = (over: Partial<StreamSession>): StreamSession => ({
  id: 's',
  titleId: 't',
  fileId: 'f',
  status: 'warming',
  bufferedPct: 0,
  peers: 0,
  speedBps: 0,
  hlsUrl: null,
  error: null,
  ...over,
});

describe('warmupView', () => {
  it('no session / no peers → connecting', () => {
    expect(warmupView(undefined).phase).toBe('connecting');
    expect(warmupView(s({})).phase).toBe('connecting');
  });
  it('ready and failed', () => {
    expect(warmupView(s({ status: 'ready', hlsUrl: '/x' }))).toEqual({ phase: 'ready', pct: 100, etaSec: 0 });
    expect(warmupView(s({ status: 'failed' })).phase).toBe('failed');
  });
  it('buffering clamps pct to 99 and has no ETA without a previous sample', () => {
    expect(warmupView(s({ peers: 3, bufferedPct: 120 }))).toEqual({ phase: 'buffering', pct: 99, etaSec: null });
  });
  it('ETA from the observed rate', () => {
    // 10% → 30% in 2 s = 10%/s → 70% left ≈ 7 s
    const v = warmupView(s({ peers: 5, bufferedPct: 30 }), { pct: 10, at: 1000 }, 3000);
    expect(v).toEqual({ phase: 'buffering', pct: 30, etaSec: 7 });
  });
  it('no ETA when progress did not move', () => {
    expect(warmupView(s({ peers: 5, bufferedPct: 30 }), { pct: 30, at: 1000 }, 3000).etaSec).toBeNull();
  });
});

describe('streamPollInterval', () => {
  it('polls every second while warming, stops otherwise', () => {
    expect(streamPollInterval(undefined)).toBe(1000);
    expect(streamPollInterval(s({}))).toBe(1000);
    expect(streamPollInterval(s({ status: 'ready' }))).toBe(false);
    expect(streamPollInterval(s({ status: 'failed' }))).toBe(false);
  });
});
