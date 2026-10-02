import { useEffect, useRef } from 'react';
import { hasMainButton, showMainButton, type MainButtonOptions } from '@/shared/telegram';

/**
 * Drive the native Telegram MainButton while `opts` is non-null.
 * Returns true when the native button is used — callers then hide their
 * in-page submit button (browser fallback keeps it).
 */
export function useMainButton(opts: MainButtonOptions | null, onClick: () => void): boolean {
  const native = hasMainButton();
  const cb = useRef(onClick);
  cb.current = onClick;
  const active = opts !== null;
  const text = opts?.text ?? '';
  const enabled = opts?.enabled ?? true;
  const loading = opts?.loading ?? false;

  useEffect(() => {
    if (!active || !native) return;
    return showMainButton({ text, enabled, loading }, () => cb.current());
  }, [active, native, text, enabled, loading]);

  return native;
}
