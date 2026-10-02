import { AlertCircle } from 'lucide-react';
import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { useStore } from 'zustand';
import { toastStore } from './toastStore';

export function Toast() {
  const { t } = useTranslation();
  const { key, seq } = useStore(toastStore);
  useEffect(() => {
    if (!key) return;
    const id = setTimeout(() => toastStore.setState({ key: null }), 3500);
    return () => clearTimeout(id);
  }, [key, seq]);
  if (!key) return null;
  return (
    <div className="pointer-events-none fixed inset-x-0 top-3 z-50 mx-auto flex max-w-lg justify-center px-4 pt-safe">
      <div
        role="status"
        aria-live="polite"
        className="page-enter flex items-center gap-2 rounded-2xl bg-fg px-4 py-3 text-sm font-medium text-bg shadow-card"
      >
        <AlertCircle className="h-4 w-4 shrink-0" aria-hidden />
        {t(key)}
      </div>
    </div>
  );
}
