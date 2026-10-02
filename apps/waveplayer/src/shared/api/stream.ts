import { ApiError } from './client';
import type { StreamPending, StreamUrl } from './types';

/** How long a play tap may wait for an acquisition before giving up. */
export const STREAM_WAIT_MS = 120_000;

const isReady = (r: StreamUrl | StreamPending): r is StreamUrl => typeof (r as StreamUrl).url === 'string';

const sleep = (ms: number, signal?: AbortSignal): Promise<void> =>
  new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(new DOMException('aborted', 'AbortError'));
    const t = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    const onAbort = (): void => {
      clearTimeout(t);
      reject(new DOMException('aborted', 'AbortError'));
    };
    signal?.addEventListener('abort', onAbort, { once: true });
  });

/**
 * Resolves a playable URL. A track that is not on the server yet answers 202
 * while it is fetched (ADR 0011): ask again after `retryAfterMs` until the
 * first bytes are buffered — the caller only shows its usual loading state.
 */
export async function waitForStream(
  fetchOnce: (signal?: AbortSignal) => Promise<StreamUrl | StreamPending>,
  signal?: AbortSignal,
  opts: { maxWaitMs?: number; now?: () => number; onPending?: (p: StreamPending) => void } = {},
): Promise<StreamUrl> {
  const now = opts.now ?? Date.now;
  const deadline = now() + (opts.maxWaitMs ?? STREAM_WAIT_MS);
  for (;;) {
    const r = await fetchOnce(signal);
    if (isReady(r)) return r;
    opts.onPending?.(r);
    if (now() >= deadline) throw new ApiError(504, 'acquisition timeout', 'ACQUIRE_TIMEOUT');
    await sleep(Math.min(Math.max(r.retryAfterMs || 1000, 300), 5000), signal);
  }
}
