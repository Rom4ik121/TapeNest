import { afterEach, describe, expect, it, vi } from 'vitest';
import { createTimedSignal } from './timeout';

describe('createTimedSignal (no AbortSignal.timeout — old iOS WebViews)', () => {
  afterEach(() => vi.useRealTimers());

  it('does not depend on AbortSignal.timeout/any', () => {
    const orig = {
      timeout: AbortSignal.timeout,
      any: (AbortSignal as { any?: unknown }).any,
    };
    // @ts-expect-error simulate an old WebView
    AbortSignal.timeout = undefined;
    (AbortSignal as { any?: unknown }).any = undefined;
    try {
      const t = createTimedSignal(1000);
      expect(t.signal.aborted).toBe(false);
      t.dispose();
    } finally {
      AbortSignal.timeout = orig.timeout;
      (AbortSignal as { any?: unknown }).any = orig.any;
    }
  });

  it('aborts after the timeout', () => {
    vi.useFakeTimers();
    const t = createTimedSignal(100);
    vi.advanceTimersByTime(100);
    expect(t.signal.aborted).toBe(true);
    expect(t.timedOut()).toBe(true);
  });

  it('follows an external signal and disposes the timer', () => {
    vi.useFakeTimers();
    const ext = new AbortController();
    const t = createTimedSignal(100, ext.signal);
    ext.abort();
    expect(t.signal.aborted).toBe(true);
    expect(t.timedOut()).toBe(false);
    const t2 = createTimedSignal(100);
    t2.dispose();
    vi.advanceTimersByTime(200);
    expect(t2.signal.aborted).toBe(false);
  });
});
