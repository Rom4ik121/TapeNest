import { describe, expect, it, vi } from 'vitest';
import { ApiError } from './client';
import { waitForStream } from './stream';

const ready = { url: '/api/v1/stream/tracks/x?sig=s', expiresAt: '2030-01-01T00:00:00Z' };
const pending = { state: 'downloading', progress: 0.2, retryAfterMs: 300 };

describe('waitForStream', () => {
  it('returns at once when the track is on the server', async () => {
    const fetchOnce = vi.fn().mockResolvedValue(ready);
    await expect(waitForStream(fetchOnce)).resolves.toEqual(ready);
    expect(fetchOnce).toHaveBeenCalledTimes(1);
  });

  it('polls 202 answers until the first bytes are buffered', async () => {
    vi.useFakeTimers();
    const fetchOnce = vi.fn().mockResolvedValueOnce(pending).mockResolvedValueOnce(pending).mockResolvedValue(ready);
    const onPending = vi.fn();
    const p = waitForStream(fetchOnce, undefined, { onPending });
    await vi.advanceTimersByTimeAsync(700);
    await expect(p).resolves.toEqual(ready);
    expect(fetchOnce).toHaveBeenCalledTimes(3);
    expect(onPending).toHaveBeenCalledWith(pending);
    vi.useRealTimers();
  });

  it('gives up after the deadline with ACQUIRE_TIMEOUT', async () => {
    let t = 0;
    const fetchOnce = vi.fn().mockImplementation(async () => {
      t += 1000;
      return { ...pending, retryAfterMs: 1 };
    });
    const err = await waitForStream(fetchOnce, undefined, { maxWaitMs: 2500, now: () => t }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).code).toBe('ACQUIRE_TIMEOUT');
  });

  it('stops waiting when the caller aborts (track switched)', async () => {
    const ac = new AbortController();
    const fetchOnce = vi.fn().mockResolvedValue({ ...pending, retryAfterMs: 5000 });
    const p = waitForStream(fetchOnce, ac.signal);
    await Promise.resolve();
    ac.abort();
    await expect(p).rejects.toMatchObject({ name: 'AbortError' });
  });

  it('propagates errors such as NO_SOURCES', async () => {
    const fetchOnce = vi.fn().mockRejectedValue(new ApiError(404, 'no sources', 'NO_SOURCES'));
    await expect(waitForStream(fetchOnce)).rejects.toMatchObject({ code: 'NO_SOURCES' });
  });
});
