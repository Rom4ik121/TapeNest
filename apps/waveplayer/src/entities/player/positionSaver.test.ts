import { afterEach, describe, expect, it, vi } from 'vitest';
import { createPositionSaver } from './positionSaver';

describe('positionSaver', () => {
  afterEach(() => vi.useRealTimers());

  it('debounces and saves the latest value', () => {
    vi.useFakeTimers();
    const save = vi.fn(async () => undefined);
    const s = createPositionSaver(save, 5000);
    s.update('a', 1);
    s.update('a', 2);
    vi.advanceTimersByTime(4999);
    expect(save).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(save).toHaveBeenCalledWith('a', 2, false);
  });

  it('switching tracks flushes the previous track immediately', () => {
    const save = vi.fn(async () => undefined);
    const s = createPositionSaver(save, 5000);
    s.update('a', 10);
    s.update('b', 1);
    expect(save).toHaveBeenCalledWith('a', 10, false);
  });

  it('flush(keepalive) passes the flag and skips duplicates', () => {
    const save = vi.fn(async () => undefined);
    const s = createPositionSaver(save, 5000);
    s.update('a', 10);
    s.flush(true);
    s.update('a', 10.2);
    s.flush(true);
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith('a', 10, true);
  });

  it('swallows save errors', async () => {
    const s = createPositionSaver(() => Promise.reject(new Error('offline')), 10);
    s.update('a', 1);
    expect(() => s.flush()).not.toThrow();
  });
});
