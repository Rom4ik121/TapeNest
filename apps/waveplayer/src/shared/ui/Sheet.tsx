import { useEffect, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { showBackButton } from '@/shared/telegram';

interface Props {
  open: boolean;
  onClose: () => void;
  title?: string;
  children: ReactNode;
}

/** Bottom sheet. Closes on backdrop tap, Escape and the Telegram Back button. */
export function Sheet({ open, onClose, title, children }: Props) {
  const { t } = useTranslation();
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    const offBack = showBackButton(onClose);
    return () => {
      window.removeEventListener('keydown', onKey);
      offBack();
    };
  }, [open, onClose]);

  if (!open) return null;
  return createPortal(
    <div className="fixed inset-0 z-[60] flex items-end justify-center">
      <button
        type="button"
        aria-label={t('a11y.closeSheet')}
        tabIndex={-1}
        className="absolute inset-0 animate-fade-in bg-ink/50"
        onClick={onClose}
      />
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="relative w-full max-w-lg animate-slide-up rounded-t-4xl bg-surface px-4 pb-safe pt-3 shadow-card"
      >
        <div aria-hidden className="mx-auto mb-3 h-1.5 w-10 rounded-full bg-fg/15" />
        {title && <h2 className="mb-2 px-2 text-lg font-bold">{title}</h2>}
        {children}
      </div>
    </div>,
    document.body,
  );
}
