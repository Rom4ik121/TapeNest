// @vitest-environment node
import { http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
// Node env has no localStorage; the persisted auth store needs one at import time.
vi.hoisted(() => {
  const m = new Map<string, string>();
  (globalThis as { localStorage?: Storage }).localStorage = {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    removeItem: (k: string) => void m.delete(k),
    clear: () => m.clear(),
    key: () => null,
    get length() {
      return m.size;
    },
  };
});

import type { AuthTokens, User } from '@/shared/api/types';
import type { TelegramContext } from '@/shared/telegram';
import { useAuthStore } from './authStore';
import { ensureSession } from './session';

const user: User = {
  id: '3f0c1d2e-8a4b-4c5d-9e6f-0123456789ab',
  telegramId: 42,
  firstName: 'Roma',
  lastName: null,
  username: null,
  photoUrl: null,
  languageCode: 'ru',
};
const tg: TelegramContext = {
  inTelegram: true,
  initDataRaw: 'query_id=x&user=%7B%22id%22%3A42%7D&hash=abc',
  user: { id: 42, firstName: 'Roma', languageCode: 'ru' },
  isDark: null,
};
const tokens = (n: string): AuthTokens => ({ accessToken: `a${n}`, refreshToken: `r${n}`, expiresIn: 900, user });

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());
beforeEach(() => {
  useAuthStore.setState({
    accessToken: null,
    refreshToken: null,
    user: null,
    identity: null,
    status: 'idle',
    error: null,
  });
});

const seed = (n: string) => useAuthStore.getState().setSession(tokens(n), 'tg:42');

describe('ensureSession against the gateway', () => {
  it('validates a stored session with /me and refreshes an expired access token (rotation)', async () => {
    seed('1');
    const calls: string[] = [];
    server.use(
      http.get('*/api/v1/me', ({ request }) => {
        const auth = request.headers.get('authorization');
        calls.push(`me:${auth}`);
        return auth === 'Bearer a2'
          ? HttpResponse.json({ ...user, firstName: 'Fresh' })
          : HttpResponse.json({ code: 'TOKEN_EXPIRED', message: 'expired' }, { status: 401 });
      }),
      http.post('*/api/v1/auth/refresh', async ({ request }) => {
        const body = (await request.json()) as { refreshToken: string };
        calls.push(`refresh:${body.refreshToken}`);
        return HttpResponse.json(tokens('2'));
      }),
    );
    await ensureSession({ tg, mockAuth: false });
    const st = useAuthStore.getState();
    expect(st.status).toBe('ready');
    expect(st.accessToken).toBe('a2');
    expect(st.refreshToken).toBe('r2');
    expect(st.user?.firstName).toBe('Fresh');
    expect(calls).toEqual(['me:Bearer a1', 'refresh:r1', 'me:Bearer a2']);
  });

  it('re-logs in with initData when the refresh token was revoked/reused', async () => {
    seed('1');
    let logins = 0;
    server.use(
      http.get('*/api/v1/me', ({ request }) =>
        request.headers.get('authorization') === 'Bearer a3'
          ? HttpResponse.json(user)
          : HttpResponse.json({ code: 'UNAUTHORIZED' }, { status: 401 }),
      ),
      http.post('*/api/v1/auth/refresh', () =>
        HttpResponse.json({ code: 'REFRESH_REUSED', message: 'reused' }, { status: 401 }),
      ),
      http.post('*/api/v1/auth/telegram', () => {
        logins++;
        return HttpResponse.json(tokens('3'));
      }),
    );
    await ensureSession({ tg, mockAuth: false });
    expect(useAuthStore.getState().status).toBe('ready');
    expect(useAuthStore.getState().accessToken).toBe('a3');
    expect(logins).toBe(1);
  });

  it('keeps the cached session when the gateway is unreachable (offline start)', async () => {
    seed('1');
    server.use(http.get('*/api/v1/me', () => HttpResponse.error()));
    await ensureSession({ tg, mockAuth: false });
    expect(useAuthStore.getState().status).toBe('ready');
    expect(useAuthStore.getState().accessToken).toBe('a1');
  });

  it('fresh login: 503 → "unavailable", 401 → "failed"', async () => {
    server.use(
      http.post('*/api/v1/auth/telegram', () => HttpResponse.json({ code: 'SERVICE_UNAVAILABLE' }, { status: 503 })),
    );
    await ensureSession({ tg, mockAuth: false });
    expect(useAuthStore.getState().error).toBe('unavailable');

    server.use(
      http.post('*/api/v1/auth/telegram', () => HttpResponse.json({ code: 'INVALID_INIT_DATA' }, { status: 401 })),
    );
    await ensureSession({ tg, mockAuth: false });
    expect(useAuthStore.getState().error).toBe('failed');
  });

  it('plain browser with real auth → "open in Telegram"; with mocked auth → guest login', async () => {
    const outside: TelegramContext = { inTelegram: false, initDataRaw: '', user: null, isDark: null };
    await ensureSession({ tg: outside, mockAuth: false });
    expect(useAuthStore.getState().error).toBe('openInTelegram');

    server.use(http.post('*/api/v1/auth/telegram', () => HttpResponse.json(tokens('g'))));
    await ensureSession({ tg: outside, mockAuth: true });
    expect(useAuthStore.getState().status).toBe('ready');
    expect(useAuthStore.getState().identity).toBe('guest');
  });
});
