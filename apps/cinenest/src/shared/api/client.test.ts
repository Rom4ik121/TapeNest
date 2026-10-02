// @vitest-environment node
// (Node env: jsdom's AbortSignal is rejected by Node's fetch used by msw/node.)
import { http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { api, ApiError, buildUrl, setAuthBridge } from './client';

const server = setupServer();
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

describe('api client', () => {
  it('builds absolute URLs with query params, skipping empty values', () => {
    expect(buildUrl('/tracks/popular', { cursor: null, limit: 20, q: '' })).toBe(
      'http://api.test/api/v1/tracks/popular?limit=20',
    );
  });

  it('sends the bearer token and parses JSON', async () => {
    setAuthBridge({ getAccessToken: () => 'tok', refresh: async () => false });
    server.use(
      http.get('*/api/v1/me', ({ request }) => HttpResponse.json({ auth: request.headers.get('authorization') })),
    );
    await expect(api<{ auth: string }>('/me')).resolves.toEqual({
      auth: 'Bearer tok',
    });
  });

  it('shares ONE refresh between concurrent 401s and retries once', async () => {
    let token = 'old';
    const refresh = vi.fn(async () => {
      await new Promise((r) => setTimeout(r, 10));
      token = 'new';
      return true;
    });
    setAuthBridge({ getAccessToken: () => token, refresh });
    server.use(
      http.get('*/api/v1/x', ({ request }) =>
        request.headers.get('authorization') === 'Bearer new'
          ? HttpResponse.json({ ok: true })
          : HttpResponse.json({ message: 'expired' }, { status: 401 }),
      ),
    );
    const results = await Promise.all([api('/x'), api('/x'), api('/x')]);
    expect(results).toEqual([{ ok: true }, { ok: true }, { ok: true }]);
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it('throws ApiError with code/service; 503 is "unavailable"', async () => {
    setAuthBridge({ getAccessToken: () => null, refresh: async () => false });
    server.use(
      http.get('*/api/v1/down', () =>
        HttpResponse.json(
          {
            message: 'music down',
            code: 'SERVICE_UNAVAILABLE',
            service: 'music',
          },
          { status: 503 },
        ),
      ),
    );
    const err = await api('/down').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).isUnavailable).toBe(true);
    expect((err as ApiError).service).toBe('music');
  });

  it('returns undefined for 204', async () => {
    server.use(http.post('*/api/v1/like', () => new HttpResponse(null, { status: 204 })));
    await expect(api('/like', { method: 'POST' })).resolves.toBeUndefined();
  });
});
