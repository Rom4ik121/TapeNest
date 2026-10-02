import type { Track } from '@/shared/api/types';

export const track = (id: string, durationSec = 100): Track => ({
  id,
  title: `Title ${id}`,
  artist: 'Artist',
  album: null,
  coverUrl: null,
  durationSec,
  liked: false,
});

export function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

export const flush = (): Promise<void> => new Promise((r) => setTimeout(r, 0));
