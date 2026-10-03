import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { StreamUrl, Track } from '@/shared/api/types';
import { FakeEngine } from '../../../test/fakeEngine';
import { deferred, flush, track } from '../../../test/fixtures';
import { BACK_DOUBLE_PRESS_MS, createPlayerStore, type PlayerDeps } from './playerStore';

function setup(overrides: Partial<PlayerDeps> = {}) {
  const engine = new FakeEngine();
  const deps = {
    engine,
    streamUrl: vi.fn(
      async (id: string): Promise<StreamUrl> => ({
        url: `blob:${id}`,
        expiresAt: '',
      }),
    ),
    like: vi.fn(async () => undefined),
    unlike: vi.fn(async () => undefined),
    reportListened: vi.fn(async () => undefined),
    savePosition: vi.fn(async () => undefined),
    waveStart: vi.fn(async () => ({
      sessionId: 'ws1',
      tracks: [track('w1'), track('w2')],
    })),
    waveMore: vi.fn(async () => [track('w3'), track('w4')]),
    waveFeedback: vi.fn(async () => undefined),
    popular: vi.fn(async () => ({
      items: [track('p1'), track('p2')],
      nextCursor: null,
    })),
    positionDebounceMs: 5000,
    ...overrides,
  } satisfies PlayerDeps;
  const store = createPlayerStore(deps);
  return { store, engine, deps };
}

const tracks: Track[] = [track('a'), track('b'), track('c')];

describe('playerStore', () => {
  beforeEach(() => {
    vi.useRealTimers();
  });

  it('plays a track: status follows real media events', async () => {
    const { store, engine } = setup();
    store.getState().playTrack(tracks[1]!, tracks);
    expect(store.getState().status).toBe('loading');
    await flush();
    expect(engine.src).toBe('blob:b');
    expect(store.getState().status).toBe('playing');
    expect(store.getState().index).toBe(1);
  });

  it('a not-yet-available track: loading until its stream is ready, then plays (ADR 0011)', async () => {
    const ready = deferred<StreamUrl>();
    const { store, engine } = setup({ streamUrl: vi.fn(() => ready.promise) });
    store.getState().playTrack(tracks[0]!, tracks);
    await flush();
    expect(store.getState().status).toBe('loading');
    expect(engine.src).toBeNull();
    ready.resolve({ url: 'blob:a', expiresAt: '' });
    await flush();
    expect(store.getState().status).toBe('playing');
  });

  it('reports a failed start (e.g. NO_SOURCES) and moves on', async () => {
    const onStreamError = vi.fn();
    const err = Object.assign(new Error('no sources'), { code: 'NO_SOURCES' });
    const { store, engine } = setup({
      onStreamError,
      streamUrl: vi.fn(async (id: string) => {
        if (id === 'a') throw err;
        return { url: `blob:${id}`, expiresAt: '' };
      }),
    });
    store.getState().playQueue(tracks, 0);
    await flush();
    expect(onStreamError).toHaveBeenCalledWith('a', err);
    await flush();
    expect(engine.src).toBe('blob:b');
  });

  it('autoplay rejected → "blocked", never "playing" (old bug)', async () => {
    const { store, engine } = setup();
    engine.allowPlay = false;
    store.getState().playQueue(tracks, 0);
    await flush();
    expect(store.getState().status).toBe('blocked');
    // A user tap retries play on the already loaded source.
    engine.allowPlay = true;
    store.getState().toggle();
    await flush();
    expect(store.getState().status).toBe('playing');
    expect(engine.loads).toHaveLength(1);
  });

  it('ignores a stale stream-url response after a fast track switch (old race)', async () => {
    const slow = deferred<StreamUrl>();
    const { store, engine } = setup({
      streamUrl: vi.fn((id: string) =>
        id === 'a' ? slow.promise : Promise.resolve({ url: `blob:${id}`, expiresAt: '' }),
      ),
    });
    store.getState().playQueue(tracks, 0); // request for "a" hangs
    store.getState().playQueue(tracks, 1); // user switches to "b"
    await flush();
    expect(engine.src).toBe('blob:b');
    slow.resolve({ url: 'blob:a', expiresAt: '' }); // late answer for "a"
    await flush();
    expect(engine.src).toBe('blob:b');
    expect(engine.loads).toEqual(['blob:b']);
    expect(store.getState().queue[store.getState().index]?.id).toBe('b');
  });

  it('reports 50% and completion once per play, and again on replay (dedupe reset)', async () => {
    const { store, engine, deps } = setup();
    store.getState().playQueue([track('a', 100)], 0);
    await flush();
    engine.tick(40, 100);
    engine.tick(55, 100);
    engine.tick(70, 100);
    expect(deps.reportListened).toHaveBeenCalledTimes(1);
    expect(deps.reportListened).toHaveBeenLastCalledWith('a', 55, false);
    engine.emit('ended');
    expect(deps.reportListened).toHaveBeenCalledTimes(2);
    expect(deps.reportListened).toHaveBeenLastCalledWith('a', 100, true);

    // replay the same track → counted again
    store.getState().playQueue([track('a', 100)], 0);
    await flush();
    engine.tick(60, 100);
    expect(deps.reportListened).toHaveBeenCalledTimes(3);
  });

  it('force-saves the position on pause and on track switch', async () => {
    const { store, engine, deps } = setup();
    store.getState().playQueue(tracks, 0);
    await flush();
    engine.tick(12, 100);
    engine.pause(); // → 'pause' event
    expect(deps.savePosition).toHaveBeenLastCalledWith('a', 12, false);

    store.getState().toggle();
    await flush();
    engine.tick(20, 100);
    await store.getState().next();
    expect(deps.savePosition).toHaveBeenLastCalledWith('a', 20, false);
  });

  it('flushPosition(keepalive) is used for visibilitychange/pagehide', async () => {
    const { store, engine, deps } = setup();
    store.getState().playQueue(tracks, 0);
    await flush();
    engine.tick(33, 100);
    store.getState().flushPosition(true);
    expect(deps.savePosition).toHaveBeenLastCalledWith('a', 33, true);
  });

  it('debounces position saves while playing (5 s)', async () => {
    vi.useFakeTimers();
    const { store, engine, deps } = setup();
    store.getState().playQueue(tracks, 0);
    await vi.advanceTimersByTimeAsync(0);
    engine.tick(1, 100);
    engine.tick(2, 100);
    engine.tick(3, 100);
    expect(deps.savePosition).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(5000);
    expect(deps.savePosition).toHaveBeenCalledTimes(1);
    expect(deps.savePosition).toHaveBeenLastCalledWith('a', 3, false);
  });

  it('wave: skip sends feedback, prefetches near the end and continues', async () => {
    const { store, deps } = setup();
    await store.getState().startWave();
    await flush();
    expect(store.getState().waveSessionId).toBe('ws1');
    await store.getState().next(); // manual skip w1 → w2 (≤5 left → prefetch)
    await flush();
    expect(deps.waveFeedback).toHaveBeenCalledWith('ws1', 'w1', 'skip');
    expect(deps.waveMore).toHaveBeenCalledWith('ws1', 'w2');
    expect(store.getState().queue.map((t) => t.id)).toEqual(['w1', 'w2', 'w3', 'w4']);
  });

  it('wave: like sends like feedback', async () => {
    const { store, deps } = setup();
    await store.getState().startWave();
    await flush();
    store.getState().toggleLikeCurrent();
    expect(deps.like).toHaveBeenCalledWith('w1');
    expect(deps.waveFeedback).toHaveBeenCalledWith('ws1', 'w1', 'like');
    expect(store.getState().queue[0]?.liked).toBe(true);
  });

  it('wave: mode is sent to the backend and remembered for restarts', async () => {
    const { store, deps } = setup();
    await store.getState().startWave('calm');
    await flush();
    expect(deps.waveStart).toHaveBeenLastCalledWith('calm');
    expect(store.getState().waveMode).toBe('calm');
    await store.getState().startWave();
    expect(deps.waveStart).toHaveBeenLastCalledWith('calm');
  });

  it('reports an early manual skip (before 50 %), never an auto-advance or a late skip', async () => {
    const reportSkipped = vi.fn(async () => undefined);
    const { store, engine } = setup({ reportSkipped });
    store.getState().playQueue(tracks, 0);
    await flush();
    engine.tick(12, 100);
    await store.getState().next(); // a → b at 12 s
    expect(reportSkipped).toHaveBeenCalledWith('a', 12);
    await flush();
    engine.tick(80, 100);
    await store.getState().next(); // b → c at 80 %: a listen, not a skip
    await store.getState().next(true); // auto-advance
    expect(reportSkipped).toHaveBeenCalledTimes(1);
  });

  it('wave unavailable → falls back to popular', async () => {
    const { store } = setup({
      waveStart: vi.fn(async () => Promise.reject(new Error('503'))),
    });
    await store.getState().startWave();
    await flush();
    expect(store.getState().waveFallback).toBe(true);
    expect(store.getState().waveSessionId).toBeNull();
    expect(store.getState().queue[0]?.id).toBe('p1');
  });

  it('stream-url failure skips to the next track, bounded', async () => {
    const { store, engine } = setup({
      streamUrl: vi.fn(async (id: string) => {
        if (id === 'a') throw new Error('404');
        return { url: `blob:${id}`, expiresAt: '' };
      }),
    });
    store.getState().playQueue(tracks, 0);
    await flush();
    await flush();
    expect(engine.src).toBe('blob:b');
  });

  it('restore(): resumes the last session from the saved position on play', async () => {
    const { store, engine } = setup();
    store.getState().restore({
      queue: tracks,
      index: 2,
      positionSec: 42,
      savedAt: Date.now(),
    });
    expect(store.getState().status).toBe('paused');
    expect(store.getState().positionSec).toBe(42);
    store.getState().toggle();
    await flush();
    expect(engine.src).toBe('blob:c');
    expect(engine.position).toBe(42);
  });

  it('prev(): after more than 10 s one press restarts, a second press skips back', async () => {
    const { store, engine } = setup();
    store.getState().playQueue(tracks, 1);
    await flush();
    engine.tick(10.1, 100);
    store.getState().prev();
    expect(store.getState().index).toBe(1);
    expect(store.getState().positionSec).toBe(0);
    expect(engine.position).toBe(0);

    store.getState().prev();
    expect(store.getState().index).toBe(0);
    expect(store.getState().positionSec).toBe(0);
    // The element can still report the track we just left.
    engine.tick(10.1, 100);
    expect(store.getState().positionSec).toBe(0);
    await flush();
    expect(store.getState().positionSec).toBe(0);
    expect(engine.src).toBe('blob:a');
  });

  it('prev(): at 10 s or less one press goes to the previous track at 0', async () => {
    const { store, engine } = setup();
    store.getState().playQueue(tracks, 1);
    await flush();
    engine.tick(10, 100);
    store.getState().prev();
    expect(store.getState().index).toBe(0);
    expect(store.getState().positionSec).toBe(0);
    engine.tick(10, 100);
    expect(store.getState().positionSec).toBe(0);
    await flush();
    expect(engine.src).toBe('blob:a');
    expect(store.getState().positionSec).toBe(0);
  });

  it('prev(): a double press near the start skips only one track', async () => {
    const { store, engine } = setup();
    store.getState().playQueue(tracks, 2);
    await flush();
    engine.tick(4, 100);
    store.getState().prev();
    expect(store.getState().index).toBe(1);
    expect(store.getState().positionSec).toBe(0);
    store.getState().prev();
    expect(store.getState().index).toBe(1);
    expect(store.getState().positionSec).toBe(0);
  });

  it('prev(): after the double-press window, back skips another track', async () => {
    vi.useFakeTimers();
    const { store, engine } = setup();
    store.getState().playQueue(tracks, 2);
    await vi.advanceTimersByTimeAsync(0);
    engine.tick(4, 100);
    store.getState().prev();
    expect(store.getState().index).toBe(1);
    await vi.advanceTimersByTimeAsync(BACK_DOUBLE_PRESS_MS + 1);
    store.getState().prev();
    expect(store.getState().index).toBe(0);
    expect(store.getState().positionSec).toBe(0);
  });

  it('resets the playhead when the track changes and ignores the previous position while loading', async () => {
    const gate = deferred<StreamUrl>();
    let calls = 0;
    const { store, engine } = setup({
      streamUrl: vi.fn((id: string) => {
        calls += 1;
        if (calls > 1) return gate.promise;
        return Promise.resolve({ url: `blob:${id}`, expiresAt: '' });
      }),
    });
    store.getState().playQueue(tracks, 0);
    await flush();
    engine.tick(42, 180);
    expect(store.getState().positionSec).toBe(42);

    const pending = store.getState().next();
    expect(store.getState().index).toBe(1);
    expect(store.getState().status).toBe('loading');
    expect(store.getState().positionSec).toBe(0);
    expect(store.getState().durationSec).toBe(tracks[1]!.durationSec);

    engine.tick(42, 180);
    expect(store.getState().positionSec).toBe(0);
    expect(store.getState().durationSec).toBe(tracks[1]!.durationSec);

    gate.resolve({ url: 'blob:b', expiresAt: '' });
    await pending;
    await flush();
    expect(store.getState().status).toBe('playing');
    expect(store.getState().positionSec).toBe(0);
    engine.tick(2, 100);
    expect(store.getState().positionSec).toBe(2);
  });

  it('keeps a restored resume position while that stream is still loading', async () => {
    const gate = deferred<StreamUrl>();
    const { store, engine } = setup({ streamUrl: vi.fn(() => gate.promise) });
    store.getState().restore({
      queue: tracks,
      index: 1,
      positionSec: 42,
      savedAt: Date.now(),
    });
    store.getState().toggle();
    expect(store.getState().status).toBe('loading');
    expect(store.getState().positionSec).toBe(42);
    engine.tick(0, 100);
    engine.tick(7, 100);
    expect(store.getState().positionSec).toBe(42);
    gate.resolve({ url: 'blob:b', expiresAt: '' });
    await flush();
    expect(engine.position).toBe(42);
    expect(store.getState().positionSec).toBe(42);
  });

  it('stops at the end of a non-wave queue', async () => {
    const { store, engine } = setup();
    store.getState().playQueue([track('x')], 0);
    await flush();
    engine.emit('ended');
    await flush();
    expect(store.getState().status).toBe('paused');
  });
});
