/**
 * Playback position persistence (spec §6): debounce 5 s while playing, plus
 * forced flush on pause / track switch / visibilitychange / pagehide.
 */
export type SavePositionFn = (trackId: string, positionSec: number, keepalive: boolean) => Promise<unknown>;

export interface PositionSaver {
  update(trackId: string, positionSec: number): void;
  flush(keepalive?: boolean): void;
  dispose(): void;
}

export function createPositionSaver(save: SavePositionFn, debounceMs = 5000): PositionSaver {
  let pending: { trackId: string; positionSec: number } | null = null;
  let lastSaved: { trackId: string; positionSec: number } | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;

  const clear = (): void => {
    if (timer !== null) clearTimeout(timer);
    timer = null;
  };

  const flush = (keepalive = false): void => {
    clear();
    if (!pending) return;
    const p = pending;
    pending = null;
    if (lastSaved && lastSaved.trackId === p.trackId && Math.abs(lastSaved.positionSec - p.positionSec) < 0.5) {
      return;
    }
    lastSaved = p;
    void save(p.trackId, p.positionSec, keepalive).catch(() => undefined);
  };

  return {
    update(trackId, positionSec) {
      // Switching tracks: the previous track's position is saved right away.
      if (pending && pending.trackId !== trackId) flush();
      pending = { trackId, positionSec };
      timer ??= setTimeout(() => {
        timer = null;
        flush();
      }, debounceMs);
    },
    flush,
    dispose: clear,
  };
}
