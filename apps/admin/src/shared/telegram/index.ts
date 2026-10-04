import {
  bindViewportCssVars,
  expandViewport,
  hapticFeedbackImpactOccurred,
  init as initSDK,
  isTMA,
  miniAppReady,
  mountMiniApp,
  mountSwipeBehavior,
  mountThemeParams,
  mountViewport,
  retrieveLaunchParams,
  setMiniAppBackgroundColor,
  setMiniAppHeaderColor,
} from '@telegram-apps/sdk-react';

export interface TelegramUserInfo {
  id: number;
  firstName: string;
  languageCode: string | null;
}

export interface TelegramContext {
  inTelegram: boolean;
  initDataRaw: string;
  user: TelegramUserInfo | null;
  isDark: boolean | null;
}

const OUTSIDE: TelegramContext = { inTelegram: false, initDataRaw: '', user: null, isDark: null };
let ctx: TelegramContext | null = null;

function attempt(fn: () => unknown): void {
  try {
    const r = fn();
    if (r && typeof (r as Promise<unknown>).catch === 'function') {
      (r as Promise<unknown>).catch(() => undefined);
    }
  } catch {
    // Optional Telegram capability.
  }
}

function readRawInitDataFallback(): string {
  try {
    const fromHash = new URLSearchParams(window.location.hash.slice(1)).get('tgWebAppData');
    if (fromHash) return fromHash;
    return new URLSearchParams(window.location.search).get('tgWebAppData') ?? '';
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
      ? { id: u.id, firstName: u.first_name ?? '', languageCode: u.language_code ?? null }
      : null;
  } catch {
    return null;
  }
}

export function initTelegram(): TelegramContext {
  if (ctx) return ctx;
  let inTelegram = false;
  try {
    inTelegram = isTMA('simple');
  } catch {
    inTelegram = false;
  }
  if (!inTelegram) {
    const raw = readRawInitDataFallback();
    ctx = raw ? { inTelegram: true, initDataRaw: raw, user: userFromRaw(raw), isDark: null } : OUTSIDE;
    return ctx;
  }
  let initDataRaw = '';
  let user: TelegramUserInfo | null = null;
  try {
    const lp = retrieveLaunchParams();
    initDataRaw = lp.initDataRaw ?? '';
    const u = lp.initData?.user;
    if (u) user = { id: u.id, firstName: u.firstName, languageCode: u.languageCode ?? null };
  } catch {
    // launch params unreadable
  }
  const fromUrl = readRawInitDataFallback();
  if (fromUrl) {
    initDataRaw = fromUrl;
    user = userFromRaw(fromUrl);
  }
  try {
    initSDK();
  } catch {
    // SDK unavailable
  }
  attempt(() => mountMiniApp());
  attempt(() => mountThemeParams());
  attempt(() => mountSwipeBehavior());
  attempt(() =>
    Promise.resolve(mountViewport()).then(() => {
      if (expandViewport.isAvailable()) expandViewport();
      if (bindViewportCssVars.isAvailable()) bindViewportCssVars();
    }),
  );
  attempt(() => miniAppReady());
  ctx = { inTelegram: true, initDataRaw, user, isDark: null };
  return ctx;
}

export function syncTelegramChrome(hex: `#${string}`): void {
  if (!ctx?.inTelegram) return;
  attempt(() => setMiniAppHeaderColor.isAvailable() && setMiniAppHeaderColor(hex));
  attempt(() => setMiniAppBackgroundColor.isAvailable() && setMiniAppBackgroundColor(hex));
}

export function haptic(): void {
  if (!ctx?.inTelegram) return;
  attempt(() => hapticFeedbackImpactOccurred('light'));
}
