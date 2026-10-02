import { renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { __resetTelegramForTests, hasMainButton, initTelegram, showMainButton } from '@/shared/telegram';
import { useMainButton } from './useMainButton';

describe('MainButton outside Telegram', () => {
  it('reports no native button so pages render the in-page fallback', () => {
    const onClick = vi.fn();
    const { result, rerender, unmount } = renderHook(
      ({ on }) => useMainButton(on ? { text: 'Create' } : null, onClick),
      {
        initialProps: { on: true },
      },
    );
    expect(result.current).toBe(false);
    rerender({ on: false });
    unmount();
    expect(onClick).not.toHaveBeenCalled();
  });

  it('showMainButton is a safe no-op', () => {
    expect(hasMainButton()).toBe(false);
    const off = showMainButton({ text: 'x' }, () => undefined);
    expect(() => off()).not.toThrow();
  });

  it('raw tgWebAppData fallback (no SDK) keeps the in-page button', () => {
    __resetTelegramForTests();
    window.location.hash = `tgWebAppData=${encodeURIComponent('user=%7B%22id%22%3A7%7D&hash=x')}`;
    try {
      expect(initTelegram().inTelegram).toBe(true);
      expect(hasMainButton()).toBe(false);
    } finally {
      window.location.hash = '';
      __resetTelegramForTests();
    }
  });
});
