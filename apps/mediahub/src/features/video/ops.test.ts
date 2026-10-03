import { describe, expect, it } from 'vitest';
import { moveClip, removeClip, splitClip, type VideoClip } from './ops';

const clip = (start: number, end: number): VideoClip => ({
  startSec: start,
  endSec: end,
  speed: 1,
  volume: 1,
  crop: { x: 0, y: 0, w: 1, h: 1 },
  rotate: 0,
  transition: 'none',
  transitionSec: 0.4,
});

describe('clip ops', () => {
  it('splits in the middle and refuses a too-short clip', () => {
    const parts = splitClip([clip(0, 4)], 0);
    expect(parts).toHaveLength(2);
    expect(parts[0]?.endSec).toBe(2);
    expect(parts[1]?.startSec).toBe(2);
    expect(splitClip([clip(0, 0.3)], 0)).toHaveLength(1);
  });

  it('reorders and keeps the last clip', () => {
    const clips = [clip(0, 1), clip(1, 2)];
    const moved = moveClip(clips, 0, 1);
    expect(moved[0]?.startSec).toBe(1);
    expect(removeClip(clips, 0)).toHaveLength(1);
    expect(removeClip([clip(0, 1)], 0)).toHaveLength(1);
  });
});
