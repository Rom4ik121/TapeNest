import { describe, expect, it } from 'vitest';
import { dayPart, formatTime, gradientFor } from './format';

describe('format', () => {
  it('formatTime', () => {
    expect(formatTime(0)).toBe('0:00');
    expect(formatTime(75.9)).toBe('1:15');
    expect(formatTime(3725)).toBe('1:02:05');
    expect(formatTime(Number.NaN)).toBe('0:00');
    expect(formatTime(-5)).toBe('0:00');
  });
  it('dayPart', () => {
    expect(dayPart(6)).toBe('morning');
    expect(dayPart(13)).toBe('day');
    expect(dayPart(20)).toBe('evening');
    expect(dayPart(2)).toBe('night');
  });
  it('gradientFor is deterministic', () => {
    expect(gradientFor('t1')).toBe(gradientFor('t1'));
  });
});
