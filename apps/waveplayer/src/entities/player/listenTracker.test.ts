import { describe, expect, it, vi } from 'vitest';
import { createListenTracker } from './listenTracker';

describe('listenTracker', () => {
  it('reports half once and completion once per begin()', () => {
    const report = vi.fn();
    const t = createListenTracker(report);
    t.begin('a');
    t.progress(10, 100);
    t.progress(50, 100);
    t.progress(80, 100);
    t.complete(100);
    t.complete(100);
    expect(report.mock.calls).toEqual([
      ['a', 50, false],
      ['a', 100, true],
    ]);
    t.begin('a');
    t.progress(51, 100);
    expect(report).toHaveBeenCalledTimes(3);
  });

  it('ignores unknown duration and calls before begin()', () => {
    const report = vi.fn();
    const t = createListenTracker(report);
    t.progress(50, 100);
    t.begin('a');
    t.progress(50, 0);
    expect(report).not.toHaveBeenCalled();
  });
});
