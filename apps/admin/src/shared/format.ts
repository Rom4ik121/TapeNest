import { ru } from '@/i18n/labels';

export function formatWhen(iso: string | null | undefined): string {
  if (!iso) return ru.never;
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return ru.never;
  return new Intl.DateTimeFormat('ru', {
    day: 'numeric',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date);
}

export function shortUrl(raw: string): string {
  try {
    const url = new URL(raw);
    const path = url.pathname.length > 28 ? `${url.pathname.slice(0, 28)}…` : url.pathname;
    return `${url.host}${path}`;
  } catch {
    return raw.length > 56 ? `${raw.slice(0, 56)}…` : raw;
  }
}
