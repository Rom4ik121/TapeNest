/** 75 → "1:15", 3725 → "1:02:05". Negative/NaN → "0:00". */
export function formatTime(totalSec: number): string {
  const s = Number.isFinite(totalSec) && totalSec > 0 ? Math.floor(totalSec) : 0;
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
}

export type DayPart = 'morning' | 'day' | 'evening' | 'night';
export function dayPart(hour: number): DayPart {
  if (hour >= 5 && hour < 12) return 'morning';
  if (hour >= 12 && hour < 18) return 'day';
  if (hour >= 18 && hour < 23) return 'evening';
  return 'night';
}

/** Deterministic pick of a brand gradient (01–05) for a track/playlist id. */
export const GRADIENT_CLASSES = ['bg-grad-02', 'bg-grad-03', 'bg-grad-04', 'bg-grad-05', 'bg-grad-01'] as const;
export function gradientFor(id: string): (typeof GRADIENT_CLASSES)[number] {
  let h = 0;
  for (const c of id) h = (Math.imul(h, 31) + c.charCodeAt(0)) | 0;
  return GRADIENT_CLASSES[Math.abs(h) % GRADIENT_CLASSES.length] ?? 'bg-grad-04';
}
