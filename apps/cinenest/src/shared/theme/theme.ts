import { initTelegram, onTelegramThemeChange, syncTelegramChrome } from '@/shared/telegram';

export type ThemeName = 'light' | 'dark';

export const THEME_BG: Record<ThemeName, `#${string}`> = {
  light: '#E7E4DE',
  dark: '#16141C',
};

/** Telegram colorScheme wins; fallback — prefers-color-scheme; default — dark. */
export function resolveTheme(telegramIsDark: boolean | null, prefersDark: boolean | null): ThemeName {
  if (telegramIsDark !== null) return telegramIsDark ? 'dark' : 'light';
  if (prefersDark !== null) return prefersDark ? 'dark' : 'light';
  return 'dark';
}

export function applyTheme(theme: ThemeName): void {
  const root = document.documentElement;
  root.dataset.theme = theme;
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', THEME_BG[theme]);
  syncTelegramChrome(THEME_BG[theme]);
}

/** Apply the theme now and keep it in sync. Returns cleanup. */
export function startThemeSync(): () => void {
  const tg = initTelegram();
  const mq = typeof window.matchMedia === 'function' ? window.matchMedia('(prefers-color-scheme: dark)') : null;
  let tgDark = tg.isDark;

  const update = (): void => applyTheme(resolveTheme(tgDark, mq ? mq.matches : null));
  update();

  const offTg = onTelegramThemeChange((dark) => {
    tgDark = dark;
    update();
  });
  const onMq = (): void => update();
  mq?.addEventListener?.('change', onMq);
  return () => {
    offTg();
    mq?.removeEventListener?.('change', onMq);
  };
}
