/**
 * "track-listened" events: once at ≥50 % and once on completion — per play.
 * `begin()` is called on every new play, so the dedupe set never grows
 * unbounded and a replayed track is counted again (old bug: global Set).
 */
export type ReportFn = (trackId: string, positionSec: number, completed: boolean) => void;

export interface ListenTracker {
  begin(trackId: string): void;
  progress(positionSec: number, durationSec: number): void;
  complete(positionSec: number): void;
  current(): string | null;
}

export function createListenTracker(report: ReportFn): ListenTracker {
  let trackId: string | null = null;
  let half = false;
  let done = false;
  return {
    begin(id) {
      trackId = id;
      half = false;
      done = false;
    },
    progress(pos, dur) {
      if (!trackId || half || dur <= 0) return;
      if (pos >= dur * 0.5) {
        half = true;
        report(trackId, pos, false);
      }
    },
    complete(pos) {
      if (!trackId || done) return;
      done = true;
      report(trackId, pos, true);
    },
    current: () => trackId,
  };
}
