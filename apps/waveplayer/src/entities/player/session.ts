import type { Track } from '@/shared/api/types';

/** Last player session in localStorage — lets the app resume after reopen. */
const KEY = 'waveplayer:session:v1';
const MAX_QUEUE = 100;

export interface StoredSession {
  queue: Track[];
  index: number;
  positionSec: number;
  savedAt: number;
}

export function saveSession(queue: Track[], index: number, positionSec: number): void {
  try {
    if (queue.length === 0) {
      localStorage.removeItem(KEY);
      return;
    }
    // Keep a window around the current track so storage stays small.
    const start = Math.max(0, Math.min(index - 10, queue.length - MAX_QUEUE));
    const slice = queue.slice(start, start + MAX_QUEUE);
    const data: StoredSession = {
      queue: slice,
      index: index - start,
      positionSec,
      savedAt: Date.now(),
    };
    localStorage.setItem(KEY, JSON.stringify(data));
  } catch {
    // storage full / disabled
  }
}

export function loadSession(): StoredSession | null {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return null;
    const data = JSON.parse(raw) as StoredSession;
    if (!Array.isArray(data.queue) || data.queue.length === 0) return null;
    if (typeof data.index !== 'number' || !data.queue[data.index]) return null;
    return data;
  } catch {
    return null;
  }
}

export function clearSession(): void {
  try {
    localStorage.removeItem(KEY);
  } catch {
    // ignore
  }
}
