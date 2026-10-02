/** 75 → "1:15", 3725 → "1:02:05". Negative/NaN → "0:00". */
export function formatTime(totalSec: number): string {
  const s = Number.isFinite(totalSec) && totalSec > 0 ? Math.floor(totalSec) : 0;
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = String(s % 60).padStart(2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`;
}

/** Bytes → localized "1,4 ГБ" / "1.4 GB" (spec §15: numbers via Intl). */
export function formatBytes(bytes: number, lang: string): string {
  const units = lang.startsWith('en') ? ['B', 'KB', 'MB', 'GB', 'TB'] : ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
  let v = Math.max(0, bytes);
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const n = new Intl.NumberFormat(lang, { maximumFractionDigits: v >= 10 || i === 0 ? 0 : 1 }).format(v);
  return `${n} ${units[i]}`;
}

/** Download speed, e.g. "3,2 МБ/с". */
export function formatSpeed(bps: number, lang: string): string {
  return `${formatBytes(bps, lang)}/${lang.startsWith('en') ? 's' : 'с'}`;
}

export function formatRating(r: number | null, lang: string): string | null {
  return r === null
    ? null
    : new Intl.NumberFormat(lang, { minimumFractionDigits: 1, maximumFractionDigits: 1 }).format(r);
}

/** Palette gradient class by id (stable). */
const GRADS = ['bg-grad-02', 'bg-grad-03', 'bg-grad-04', 'bg-grad-05'] as const;
export function gradientFor(id: string): string {
  let h = 0;
  for (const c of id) h = (Math.imul(h, 31) + c.charCodeAt(0)) | 0;
  return GRADS[Math.abs(h) % GRADS.length] ?? 'bg-grad-04';
}
