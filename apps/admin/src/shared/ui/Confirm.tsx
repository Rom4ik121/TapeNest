import { ru } from '@/i18n/labels';

export function ConfirmDialog({
  title,
  body,
  danger,
  busy,
  onCancel,
  onConfirm,
}: {
  title: string;
  body: string;
  danger?: boolean;
  busy?: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-end justify-center bg-ink/50 p-3"
      role="presentation"
      onClick={onCancel}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
        className="w-full max-w-md rounded-3xl bg-surface p-5 shadow-card"
        onClick={(event) => event.stopPropagation()}
      >
        <h2 id="confirm-title" className="text-lg font-semibold">
          {title}
        </h2>
        <p className="mt-2 text-sm text-muted">{body}</p>
        <div className="mt-5 grid grid-cols-2 gap-2">
          <button
            type="button"
            className="rounded-2xl bg-surface-2 px-4 py-3 text-sm font-semibold"
            onClick={onCancel}
            disabled={busy}
          >
            {ru.cancel}
          </button>
          <button
            type="button"
            className={`rounded-2xl px-4 py-3 text-sm font-semibold ${danger ? 'bg-danger text-danger-fg' : 'bg-accent text-accent-fg'}`}
            onClick={onConfirm}
            disabled={busy}
          >
            {ru.confirm}
          </button>
        </div>
      </div>
    </div>
  );
}
