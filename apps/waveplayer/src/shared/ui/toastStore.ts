import { createStore } from 'zustand/vanilla';

export interface ToastState {
  /** i18n key of the visible message (null: hidden). */
  key: string | null;
  seq: number;
}

export const toastStore = createStore<ToastState>()(() => ({ key: null, seq: 0 }));

/** Shows a short, non-blocking message (i18n key) for ~3.5 s. */
export function showToast(key: string): void {
  toastStore.setState((s) => ({ key, seq: s.seq + 1 }));
}
