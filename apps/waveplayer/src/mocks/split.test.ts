// @vitest-environment node
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { authApi, tracksApi } from '@/shared/api/endpoints';
import { setAuthBridge } from '@/shared/api/client';
import { authHandlers, handlersFor, musicHandlers } from './handlers';

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const withToken = (token: string) => setAuthBridge({ getAccessToken: () => token, refresh: async () => false });

describe('per-domain mock handlers', () => {
  it('registers only the requested domains', () => {
    expect(handlersFor(new Set(['music']))).toHaveLength(musicHandlers.length);
    expect(handlersFor(new Set(['auth']))).toHaveLength(authHandlers.length);
    expect(handlersFor(new Set())).toHaveLength(0);
  });

  it('music-only: accepts a real gateway JWT (mock cannot verify it)', async () => {
    server.use(...handlersFor(new Set(['music'])));
    withToken('eyJhbGciOiJIUzI1NiJ9.payload.sig');
    const page = await tracksApi.list('popular', null);
    expect(page.items.length).toBeGreaterThan(0);
  });

  it('music-only: still rejects requests without a bearer token', async () => {
    server.use(...handlersFor(new Set(['music'])));
    setAuthBridge({ getAccessToken: () => null, refresh: async () => false });
    await expect(tracksApi.list('popular', null)).rejects.toMatchObject({ status: 401 });
  });

  it('all mocked: only mock-issued tokens pass (exercises the refresh path)', async () => {
    server.use(...handlersFor(new Set(['auth', 'music'])));
    withToken('eyJhbGciOiJIUzI1NiJ9.payload.sig');
    await expect(tracksApi.list('popular', null)).rejects.toMatchObject({ status: 401 });
    const tokens = await authApi.login('');
    withToken(tokens.accessToken);
    await expect(authApi.me()).resolves.toMatchObject({ id: tokens.user.id });
    await expect(authApi.logout(tokens.refreshToken)).resolves.toBeUndefined();
    await expect(authApi.refresh(tokens.refreshToken)).rejects.toMatchObject({ status: 401 });
  });
});
