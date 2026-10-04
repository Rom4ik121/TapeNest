import { ApiError, api, setAccessToken } from '@/shared/api/client';
import type { TelegramContext } from '@/shared/telegram';

export interface AdminSession {
  telegramId: number;
  role: 'admin';
}

export type GateState = { kind: 'outside' } | { kind: 'refused' } | { kind: 'ready'; telegramId: number };

interface AuthTokens {
  accessToken: string;
}

/** Exchange Telegram initData for a token, then ask the live allowlist. A refusal drops the token. */
export async function openAdmin(tg: TelegramContext): Promise<GateState> {
  if (!tg.initDataRaw) return { kind: 'outside' };
  const tokens = await api<AuthTokens>('/auth/telegram', {
    method: 'POST',
    auth: false,
    body: { initData: tg.initDataRaw },
  });
  setAccessToken(tokens.accessToken);
  try {
    const session = await api<AdminSession>('/admin/session');
    return { kind: 'ready', telegramId: session.telegramId };
  } catch (err) {
    setAccessToken(null);
    if (err instanceof ApiError && err.status === 403) return { kind: 'refused' };
    throw err;
  }
}
