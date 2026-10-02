/**
 * The ONLY module allowed to touch the Telegram SDK (enforced by ESLint
 * `no-restricted-imports`). Everything here is safe to call in a plain
 * browser: calls either return a fallback or are silently ignored.
 *
 * SDK: @telegram-apps/sdk-react 2.0.25 → @telegram-apps/sdk 2.11.3 (spec §3.2).
 */
import {
  backButton,
  bindViewportCssVars,
  disableVerticalSwipes,
  expandViewport,
  hapticFeedbackImpactOccurred,
  hapticFeedbackNotificationOccurred,
  hapticFeedbackSelectionChanged,
  init as initSDK,
  isColorDark,
  isTMA,
  isThemeParamsDark,
  miniAppReady,
  mountBackButton,
  mountMainButton,
  mountMiniApp,
  mountSwipeBehavior,
  mountThemeParams,
  mountViewport,
  offMainButtonClick,
  onMainButtonClick,
  retrieveLaunchParams,
  setMainButtonParams,
  setMiniAppBackgroundColor,
  setMiniAppBottomBarColor,
  setMiniAppHeaderColor,
} from '@telegram-apps/sdk-react';

export interface TelegramUserInfo {
  id: number;
  firstName: string;
  languageCode: string | null;
}

export interface TelegramContext {
  inTelegram: boolean;
  /** Raw initData query string (empty outside Telegram). */
  initDataRaw: string;
  user: TelegramUserInfo | null;
  /** null → unknown (use prefers-color-scheme). */
  isDark: boolean | null;
}

const OUTSIDE: TelegramContext = {
  inTelegram: false,
  initDataRaw: '',
  user: null,
  isDark: null,
};
let ctx: TelegramContext | null = null;
/** True only when the SDK itself initialised (not the raw tgWebAppData fallback). */
let sdkReady = false;

function attempt(fn: () => unknown): void {
  try {
    const r = fn();
    if (r && typeof (r as Promise<unknown>).catch === 'function') {
      (r as Promise<unknown>).catch(() => undefined);
    }
  } catch {
    // Optional capability — never fatal.
  }
}

function readRawInitDataFallback(): string {
  try {
    const fromHash = new URLSearchParams(window.location.hash.slice(1)).get('tgWebAppData');
    if (fromHash) return fromHash;
    const fromQuery = new URLSearchParams(window.location.search).get('tgWebAppData');
    if (fromQuery) return fromQuery;
    const w = window as Window & {
      Telegram?: { WebApp?: { initData?: string } };
    };
    return w.Telegram?.WebApp?.initData ?? '';
  } catch {
    return '';
  }
}

function userFromRaw(raw: string): TelegramUserInfo | null {
  try {
    const u = JSON.parse(new URLSearchParams(raw).get('user') ?? 'null') as {
      id?: number;
      first_name?: string;
      language_code?: string;
    } | null;
    return u && typeof u.id === 'number'
      ? {
          id: u.id,
          firstName: u.first_name ?? '',
          languageCode: u.language_code ?? null,
        }
      : null;
  } catch {
    return null;
  }
}

/** Initialise the SDK once. Returns what the app needs to know about the launch. */
export function initTelegram(): TelegramContext {
  if (ctx) return ctx;
  let inTelegram = false;
  try {
    inTelegram = isTMA('simple');
  } catch {
    inTelegram = false;
  }
  if (!inTelegram) {
    // The SDK's strict parser can reject launch params from newer/older
    // clients; fall back to reading tgWebAppData ourselves so auth still works.
    const raw = readRawInitDataFallback();
    ctx = raw
      ? {
          inTelegram: true,
          initDataRaw: raw,
          user: userFromRaw(raw),
          isDark: null,
        }
      : OUTSIDE;
    return ctx;
  }

  let initDataRaw = '';
  let user: TelegramUserInfo | null = null;
  let isDark: boolean | null = null;
  try {
    const lp = retrieveLaunchParams();
    initDataRaw = lp.initDataRaw ?? '';
    const u = lp.initData?.user;
    if (u)
      user = {
        id: u.id,
        firstName: u.firstName,
        languageCode: u.languageCode ?? null,
      };
    const bg = lp.themeParams.bgColor;
    if (bg) isDark = isColorDark(bg);
  } catch {
    // launch params unreadable — keep defaults
  }
  // A cached SDK value can be an older launch. The URL is what we opened.
  const fromUrl = readRawInitDataFallback();
  if (fromUrl) {
    initDataRaw = fromUrl;
    user = userFromRaw(fromUrl);
  }

  try {
    initSDK();
    sdkReady = true;
  } catch {
    sdkReady = false;
  }
  attempt(() => mountMiniApp());
  attempt(() => mountThemeParams());
  attempt(() => mountBackButton());
  attempt(() => mountMainButton());
  attempt(() => mountSwipeBehavior());
  attempt(() => disableVerticalSwipes()); // seek-bar drags must not close the app
  attempt(() =>
    Promise.resolve(mountViewport()).then(() => {
      // Full height from the start (player + nav don't fit the half-sheet).
      if (expandViewport.isAvailable()) expandViewport();
      // --tg-viewport-{height,stable-height,safe-area-inset-*,content-safe-area-inset-*}
      // consumed by theme.css (--app-height / --app-safe-*).
      if (bindViewportCssVars.isAvailable()) bindViewportCssVars();
    }),
  );
  attempt(() => miniAppReady());
  try {
    isDark = isThemeParamsDark() ?? isDark;
  } catch {
    // theme params not mounted
  }

  ctx = { inTelegram: true, initDataRaw, user, isDark };
  return ctx;
}

/** Subscribe to Telegram theme (colorScheme) changes. */
export function onTelegramThemeChange(cb: (isDark: boolean) => void): () => void {
  if (!ctx?.inTelegram) return () => undefined;
  try {
    return isThemeParamsDark.sub((dark) => cb(dark));
  } catch {
    return () => undefined;
  }
}

/** Paint Telegram chrome (header / background / bottom bar) with app colours. */
export function syncTelegramChrome(hex: `#${string}`): void {
  if (!ctx?.inTelegram) return;
  attempt(() => setMiniAppHeaderColor.isAvailable() && setMiniAppHeaderColor(hex));
  attempt(() => setMiniAppBackgroundColor.isAvailable() && setMiniAppBackgroundColor(hex));
  attempt(() => setMiniAppBottomBarColor.isAvailable() && setMiniAppBottomBarColor(hex));
}

/**
 * Native Back button with a handler stack: nested layers (page → sheet →
 * full player) each push a handler; only the top one runs.
 * Returns cleanup. No-op outside Telegram.
 */
const backStack: Array<() => void> = [];
let backListenerOff: (() => void) | null = null;

function refreshBackButton(): void {
  try {
    if (backStack.length > 0) {
      backButton.show();
      backListenerOff ??= backButton.onClick(() => backStack[backStack.length - 1]?.());
    } else {
      backButton.hide();
      backListenerOff?.();
      backListenerOff = null;
    }
  } catch {
    // back button unsupported
  }
}

export function showBackButton(onBack: () => void): () => void {
  if (!ctx?.inTelegram) return () => undefined;
  const entry = (): void => onBack();
  backStack.push(entry);
  refreshBackButton();
  return () => {
    const i = backStack.lastIndexOf(entry);
    if (i >= 0) backStack.splice(i, 1);
    refreshBackButton();
  };
}

export type HapticKind = 'light' | 'medium' | 'heavy' | 'success' | 'error' | 'selection';

export function haptic(kind: HapticKind = 'light'): void {
  if (!ctx?.inTelegram) return;
  attempt(() => {
    if (kind === 'success' || kind === 'error') hapticFeedbackNotificationOccurred(kind);
    else if (kind === 'selection') hapticFeedbackSelectionChanged();
    else hapticFeedbackImpactOccurred(kind);
  });
}

export interface MainButtonOptions {
  text: string;
  enabled?: boolean;
  loading?: boolean;
}

/** True when the native Telegram MainButton can be used (else render an in-page button). */
export function hasMainButton(): boolean {
  if (!ctx?.inTelegram || !sdkReady) return false;
  try {
    return setMainButtonParams.isAvailable();
  } catch {
    return false;
  }
}

/**
 * Show the native MainButton with a click handler. Returns cleanup (hides it).
 * Call again to update params; each call replaces the handler.
 */
export function showMainButton(opts: MainButtonOptions, onClick: () => void): () => void {
  if (!hasMainButton()) return () => undefined;
  const handler = (): void => {
    haptic('medium');
    onClick();
  };
  attempt(() =>
    setMainButtonParams({
      text: opts.text,
      isVisible: true,
      isEnabled: opts.enabled ?? true,
      isLoaderVisible: opts.loading ?? false,
    }),
  );
  attempt(() => onMainButtonClick(handler));
  return () => {
    attempt(() => offMainButtonClick(handler));
    attempt(() => setMainButtonParams({ isVisible: false, isLoaderVisible: false }));
  };
}

/** Test helper. */
export function __resetTelegramForTests(): void {
  ctx = null;
  sdkReady = false;
}
