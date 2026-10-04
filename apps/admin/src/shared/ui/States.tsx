import { ru } from '@/i18n/labels';

export function Loading() {
  return <p className="py-10 text-center text-sm text-muted">{ru.loading}</p>;
}

export function Failed({ onRetry, text = ru.error }: { onRetry: () => void; text?: string }) {
  return (
    <div className="rounded-3xl bg-surface p-5 text-center shadow-card">
      <p className="text-sm">{text}</p>
      <button
        type="button"
        className="mt-3 rounded-full bg-accent px-4 py-2 text-sm font-semibold text-accent-fg"
        onClick={onRetry}
      >
        {ru.retry}
      </button>
    </div>
  );
}

export function Empty({ text = ru.empty }: { text?: string }) {
  return <p className="py-8 text-center text-sm text-muted">{text}</p>;
}
