/**
 * Watch position persistence (spec §6): debounce 5 s while playing, plus forced
 * flush on pause / file switch / visibilitychange / pagehide / player close.
 */
export interface PositionSample {
  titleId: string;
  fileId: string;
  positionSec: number;
  durationSec: number;
}

export type SavePositionFn = (s: PositionSample, keepalive: boolean) => Promise<unknown>;

export interface PositionSaver {
  update(s: PositionSample): void;
  flush(keepalive?: boolean): void;
  dispose(): void;
}

export function createPositionSaver(save: SavePositionFn, debounceMs = 5000): PositionSaver {
  let pending: PositionSample | null = null;
  let lastSaved: PositionSample | null = null;
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
    if (p.positionSec < 1) return; // never overwrite a real position with "0" from a fresh load
    if (lastSaved && lastSaved.fileId === p.fileId && Math.abs(lastSaved.positionSec - p.positionSec) < 0.5) return;
    lastSaved = p;
    void save(p, keepalive).catch(() => undefined);
  };

  return {
    update(s) {
      if (pending && pending.fileId !== s.fileId) flush();
      pending = s;
      timer ??= setTimeout(() => {
        timer = null;
        flush();
      }, debounceMs);
    },
    flush,
    dispose: clear,
  };
}
