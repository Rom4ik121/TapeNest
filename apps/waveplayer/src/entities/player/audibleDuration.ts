/** Encoder delay and rounding we still treat as part of the song. */
export const DURATION_SLACK_SEC = 5;

/**
 * Catalog length is the audible song. A YouTube container often reports a
 * longer timeline, and the element keeps advancing through silence after the
 * song. Trust the catalog length when the element runs substantially past it.
 */
export function audibleDuration(catalogSec: number, mediaSec: number): number {
  const catalog = positive(catalogSec);
  if (mediaRunsPastSong(catalogSec, mediaSec)) return catalog;
  const media = positive(mediaSec);
  if (media > 0) return media;
  return catalog;
}

/** True when the element timeline is long enough past the catalog length to be silence, not the song. */
export function mediaRunsPastSong(catalogSec: number, mediaSec: number): boolean {
  const catalog = positive(catalogSec);
  if (catalog <= 0) return false;
  if (!Number.isFinite(mediaSec)) return mediaSec === Number.POSITIVE_INFINITY;
  return mediaSec > catalog + DURATION_SLACK_SEC;
}

function positive(sec: number): number {
  return Number.isFinite(sec) && sec > 0 ? sec : 0;
}
