import { describe, expect, it } from 'vitest';
import { audibleDuration, mediaRunsPastSong } from './audibleDuration';

describe('audibleDuration', () => {
  it('keeps the catalog length when the element timeline runs into silence', () => {
    expect(audibleDuration(120, 340)).toBe(120);
    expect(mediaRunsPastSong(120, 340)).toBe(true);
  });

  it('keeps a media duration that matches the song', () => {
    expect(audibleDuration(120, 122)).toBe(122);
    expect(mediaRunsPastSong(120, 122)).toBe(false);
  });

  it('falls back when one side is missing', () => {
    expect(audibleDuration(0, 180)).toBe(180);
    expect(audibleDuration(90, 0)).toBe(90);
    expect(audibleDuration(Number.NaN, Number.NaN)).toBe(0);
  });

  it('treats an infinite element duration as silence past the song', () => {
    expect(audibleDuration(120, Number.POSITIVE_INFINITY)).toBe(120);
    expect(mediaRunsPastSong(120, Number.POSITIVE_INFINITY)).toBe(true);
  });
});
