import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createPositionSaver, type PositionSample } from './positionSaver';

const p = (positionSec: number, fileId = 'f1'): PositionSample => ({
  titleId: 't',
  fileId,
  positionSec,
  durationSec: 100,
});

describe('positionSaver', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('debounces: one save with the latest sample after 5 s', () => {
    const save = vi.fn(() => Promise.resolve());
    const s = createPositionSaver(save);
    s.update(p(10));
    s.update(p(11));
    s.update(p(12));
    expect(save).not.toHaveBeenCalled();
    vi.advanceTimersByTime(5000);
    expect(save).toHaveBeenCalledTimes(1);
    expect(save).toHaveBeenCalledWith(p(12), false);
  });

  it('flush saves immediately and passes keepalive', () => {
    const save = vi.fn(() => Promise.resolve());
    const s = createPositionSaver(save);
    s.update(p(42));
    s.flush(true);
    expect(save).toHaveBeenCalledWith(p(42), true);
    vi.advanceTimersByTime(6000);
    expect(save).toHaveBeenCalledTimes(1);
  });

  it('skips positions < 1 s and unchanged positions', () => {
    const save = vi.fn(() => Promise.resolve());
    const s = createPositionSaver(save);
    s.update(p(0.3));
    s.flush();
    s.update(p(20));
    s.flush();
    s.update(p(20.2));
    s.flush();
    expect(save).toHaveBeenCalledTimes(1);
  });

  it('switching file flushes the previous one first', () => {
    const save = vi.fn(() => Promise.resolve());
    const s = createPositionSaver(save);
    s.update(p(30, 'f1'));
    s.update(p(5, 'f2'));
    expect(save).toHaveBeenCalledWith(p(30, 'f1'), false);
  });

  it('dispose cancels the pending timer; save errors are swallowed', async () => {
    const save = vi.fn(() => Promise.reject(new Error('x')));
    const s = createPositionSaver(save);
    s.update(p(30));
    s.dispose();
    vi.advanceTimersByTime(6000);
    expect(save).not.toHaveBeenCalled();
    s.flush();
    expect(save).toHaveBeenCalledTimes(1);
    await Promise.resolve();
  });
});
