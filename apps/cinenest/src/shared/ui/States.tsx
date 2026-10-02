import { CloudOff, RotateCw } from 'lucide-react';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { errorMessageKey } from '@/shared/api/errors';
import { haptic } from '@/shared/telegram';

export function ErrorState({
  error,
  onRetry,
  title,
  retrying = false,
}: {
  error?: unknown;
  onRetry?: () => void;
  title?: string;
  retrying?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div
      role="alert"
      className="page-enter flex flex-col items-center gap-3 rounded-3xl bg-surface px-6 py-8 text-center shadow-card"
    >
      <CloudOff className="h-8 w-8 text-muted" aria-hidden />
      <div>
        <p className="font-semibold">{title ?? t('error.title')}</p>
        <p className="mt-1 text-sm text-muted">{t(errorMessageKey(error))}</p>
      </div>
      {onRetry && (
        <button
          type="button"
          disabled={retrying}
          onClick={() => {
            haptic('light');
            onRetry();
          }}
          className="inline-flex min-h-[40px] items-center gap-2 rounded-full bg-accent px-4 py-2 text-sm font-semibold text-accent-fg transition active:scale-95 disabled:opacity-70"
        >
          <RotateCw className={retrying ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} aria-hidden />
          {retrying ? t('error.retrying') : t('error.retry')}
        </button>
      )}
    </div>
  );
}

export function EmptyState({ icon, children }: { icon?: ReactNode; children: ReactNode }) {
  return (
    <div className="page-enter flex flex-col items-center gap-3 px-6 py-10 text-center text-muted">
      {icon}
      <p className="text-sm">{children}</p>
    </div>
  );
}
