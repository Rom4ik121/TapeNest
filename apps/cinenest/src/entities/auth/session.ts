import { ApiError, setAuthBridge } from '@/shared/api/client';
import { authApi } from '@/shared/api/endpoints';
import type { User } from '@/shared/api/types';
import type { TelegramContext } from '@/shared/telegram';
import { useAuthStore } from './authStore';

export function identityOf(tg: Pick<TelegramContext, 'user'>): string {
  return tg.user ? `tg:${tg.user.id}` : 'guest';
}

/**
 * A stored JWT may only be reused by the same Telegram account it was issued
 * for (old bug: tokens survived a Telegram account switch on the device).
 */
export function isStoredSessionValid(
  stored: {
    accessToken: string | null;
    user: User | null;
    identity: string | null;
  },
  tg: Pick<TelegramContext, 'user'>,
): boolean {
  if (!stored.accessToken || !stored.user || !stored.identity) return false;
  if (stored.identity !== identityOf(tg)) return false;
  if (tg.user && stored.user.telegramId !== tg.user.id) return false;
  return true;
}

export interface AuthDeps {
  tg: TelegramContext;
  /** Auth is served by MSW → a plain browser (no initData) may log in as a guest. */
  mockAuth: boolean;
}

async function loginWithInitData(tg: TelegramContext): Promise<void> {
  const tokens = await authApi.login(tg.initDataRaw);
  useAuthStore.getState().setSession(tokens, identityOf(tg));
}

let inFlight: Promise<void> | null = null;

/**
 * Establish a session: reuse valid stored tokens or exchange initData for JWT.
 * Concurrent calls (React StrictMode double effects) share one attempt.
 */
export function ensureSession(deps: AuthDeps): Promise<void> {
  inFlight ??= doEnsureSession(deps).finally(() => {
    inFlight = null;
  });
  return inFlight;
}

async function doEnsureSession({ tg, mockAuth }: AuthDeps): Promise<void> {
  const st = useAuthStore.getState();
  setAuthBridge({
    getAccessToken: () => useAuthStore.getState().accessToken,
    refresh: async () => {
      const rt = useAuthStore.getState().refreshToken;
      try {
        if (!rt) throw new Error('no refresh token');
        useAuthStore.getState().setSession(await authApi.refresh(rt), identityOf(tg));
        return true;
      } catch {
        // Refresh token expired/revoked → re-login with (still valid) initData.
        useAuthStore.getState().clearSession();
        if (!tg.initDataRaw && !mockAuth) return false;
        try {
          await loginWithInitData(tg);
          return true;
        } catch {
          return false;
        }
      }
    },
  });

  st.setStatus('loading');
  if (isStoredSessionValid(st, tg)) {
    // Validate against the gateway: an expired access token goes through the
    // shared 401 → refresh (rotation) → retry path; a revoked one → re-login.
    try {
      useAuthStore.getState().setUser(await authApi.me());
      useAuthStore.getState().setStatus('ready');
      return;
    } catch (e) {
      if (e instanceof ApiError && e.isUnavailable) {
        // Offline / gateway down: keep the cached session, requests will retry.
        useAuthStore.getState().setStatus('ready');
        return;
      }
    }
  }
  useAuthStore.getState().clearSession();
  if (!tg.inTelegram && !mockAuth) {
    useAuthStore.getState().setStatus('error', 'openInTelegram');
    return;
  }
  try {
    await loginWithInitData(tg);
    useAuthStore.getState().setStatus('ready');
  } catch (e) {
    useAuthStore.getState().setStatus('error', e instanceof ApiError && e.isUnavailable ? 'unavailable' : 'failed');
  }
}
