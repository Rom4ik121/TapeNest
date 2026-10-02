/** "1:05" → 65, "1:02:03" → 3723, "90" → 90. Invalid clocks return null. */
export function parseClock(raw: string): number | null {
  const s = raw.trim();
  if (!/^\d{1,3}(:\d{1,2}){0,2}$/.test(s)) return null;
  const parts = s.split(':').map((p) => Number(p));
  if (parts.some((n) => !Number.isInteger(n))) return null;
  if (parts.length === 1) return parts[0] ?? null;
  if (parts.length === 2) {
    const [m, sec] = parts;
    if (m === undefined || sec === undefined || sec > 59) return null;
    return m * 60 + sec;
  }
  const [h, m, sec] = parts;
  if (h === undefined || m === undefined || sec === undefined || m > 59 || sec > 59) return null;
  return h * 3600 + m * 60 + sec;
}

export interface TrimRange {
  startSec: number;
  endSec: number;
}

/** A fragment at least 1s long, inside duration when duration is known (> 0). */
export function trimRange(startRaw: string, endRaw: string, durationSec: number): TrimRange | null {
  const startSec = parseClock(startRaw);
  const endSec = parseClock(endRaw);
  if (startSec === null || endSec === null) return null;
  if (endSec - startSec < 1) return null;
  if (durationSec > 0 && endSec > durationSec) return null;
  return { startSec, endSec };
}

export function formatWhen(iso: string, locale: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return new Intl.DateTimeFormat(locale.startsWith('en') ? 'en' : 'ru', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
  }).format(d);
}
