import { describe, expect, it } from 'vitest';
import { ApiError } from '@/shared/api/client';
import { downloadErrorKey } from './api';
import { formatWhen, parseClock, trimRange } from './range';

describe('video clocks', () => {
  it('parses mm:ss, h:mm:ss and bare seconds', () => {
    expect(parseClock('1:05')).toBe(65);
    expect(parseClock('1:02:03')).toBe(3723);
    expect(parseClock('90')).toBe(90);
    expect(parseClock('1:60')).toBeNull();
    expect(parseClock('')).toBeNull();
  });

  it('rejects a fragment shorter than a second or past the end', () => {
    expect(trimRange('0:00', '0:00', 30)).toBeNull();
    expect(trimRange('0:10', '0:40', 30)).toBeNull();
    expect(trimRange('0:02', '0:08', 30)).toEqual({ startSec: 2, endSec: 8 });
  });

  it('formats a date in Russian', () => {
    const text = formatWhen('2026-10-02T12:00:00Z', 'ru');
    expect(text).toMatch(/2026/);
    expect(text).toMatch(/2/);
  });

  it('maps download errors to library copy', () => {
    expect(downloadErrorKey(new ApiError(400, 'x', 'UNSUPPORTED_SOURCE'))).toBe('videos.errors.unsupported');
    expect(downloadErrorKey(new ApiError(400, 'x', 'PLAYLIST_NOT_SUPPORTED'))).toBe('videos.errors.playlist');
    expect(downloadErrorKey(new ApiError(429, 'x', 'QUOTA_DAILY'))).toBe('videos.errors.quotaDaily');
    expect(downloadErrorKey(new ApiError(422, 'x', 'EDIT_FAILED'))).toBe('videos.errors.trim');
  });
});
