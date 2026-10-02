import { describe, expect, it } from 'vitest';
import { formatBytes, formatRating, formatSpeed, formatTime, gradientFor } from './format';

const nb = (s: string) => s.replace(/\u00a0|\u202f/g, ' ');

describe('format', () => {
  it.each([
    [0, '0:00'],
    [-3, '0:00'],
    [Number.NaN, '0:00'],
    [75, '1:15'],
    [3725, '1:02:05'],
  ])('formatTime(%d) = %s', (sec, want) => expect(formatTime(sec)).toBe(want));

  it('formatBytes localizes units and decimals', () => {
    expect(nb(formatBytes(512, 'en'))).toBe('512 B');
    expect(nb(formatBytes(1.5 * 1024 ** 3, 'en'))).toBe('1.5 GB');
    expect(nb(formatBytes(1.5 * 1024 ** 3, 'ru'))).toBe('1,5 ГБ');
    expect(nb(formatBytes(20 * 1024 ** 2, 'ru'))).toBe('20 МБ');
    expect(nb(formatBytes(-1, 'en'))).toBe('0 B');
  });

  it('formatSpeed appends per-second unit', () => {
    expect(nb(formatSpeed(3 * 1024 ** 2, 'ru'))).toBe('3 МБ/с');
    expect(nb(formatSpeed(3 * 1024 ** 2, 'en'))).toBe('3 MB/s');
  });

  it('formatRating', () => {
    expect(formatRating(null, 'en')).toBeNull();
    expect(formatRating(7.25, 'ru')).toBe('7,3');
    expect(formatRating(8, 'en')).toBe('8.0');
  });

  it('gradientFor is stable and from the palette set', () => {
    expect(gradientFor('abc')).toBe(gradientFor('abc'));
    expect(['bg-grad-02', 'bg-grad-03', 'bg-grad-04', 'bg-grad-05']).toContain(gradientFor('xyz'));
  });
});
