import type { StreamSession } from '@/shared/api/types';

export type WarmupPhase = 'connecting' | 'buffering' | 'ready' | 'failed';

export interface WarmupView {
  phase: WarmupPhase;
  /** 0..100 for the progress ring */
  pct: number;
  /** Rough ETA in seconds from the current preload rate, null when unknown. */
  etaSec: number | null;
}

/**
 * TorrServer buffers the first seconds before playback (spec §5.5: "десятки секунд").
 * No peers yet → connecting; preloading → buffering with %, ETA from the observed rate.
 */
export function warmupView(
  s: StreamSession | undefined,
  prev?: { pct: number; at: number },
  now = Date.now(),
): WarmupView {
  if (!s) return { phase: 'connecting', pct: 0, etaSec: null };
  if (s.status === 'failed') return { phase: 'failed', pct: 0, etaSec: null };
  if (s.status === 'ready') return { phase: 'ready', pct: 100, etaSec: 0 };
  const pct = Math.max(0, Math.min(99, s.bufferedPct));
  if (s.peers === 0 && pct === 0) return { phase: 'connecting', pct, etaSec: null };
  let etaSec: number | null = null;
  if (prev && now > prev.at && pct > prev.pct) {
    const rate = (pct - prev.pct) / ((now - prev.at) / 1000);
    etaSec = Math.ceil((100 - pct) / rate);
  }
  return { phase: 'buffering', pct, etaSec };
}

/** Poll interval for the stream session query: 1 s while warming, stop otherwise. */
export function streamPollInterval(s: StreamSession | undefined): number | false {
  return !s || s.status === 'warming' ? 1000 : false;
}
