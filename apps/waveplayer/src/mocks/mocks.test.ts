// @vitest-environment node
// (Node env: jsdom's AbortSignal is rejected by Node's fetch used by msw/node.)
import { setupServer } from 'msw/node';
import { afterAll, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { authApi, playlistsApi, tracksApi } from '@/shared/api/endpoints';
import { setAuthBridge } from '@/shared/api/client';
import { CATALOG, mockDb, resetMockDb } from './data';
import { handlers, paginate, userFromInitData } from './handlers';
import { UUID_RE, uuidFromSeed } from './uuid';

const server = setupServer(...handlers);
let token: string | null = null;
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterAll(() => server.close());
beforeEach(async () => {
  resetMockDb();
  setAuthBridge({ getAccessToken: () => token, refresh: async () => false });
  token = (await authApi.login('')).accessToken;
});

describe('mock data module', () => {
  it('imports without a TDZ error and computes liked lazily', () => {
    expect(CATALOG.length).toBeGreaterThan(40);
    expect(mockDb.liked.has('t3')).toBe(true);
  });
});

describe('mock auth', () => {
  it('browser (empty initData) → guest user with UUID id', () => {
    const u = userFromInitData('');
    expect(u.telegramId).toBe(0);
    expect(u.id).toMatch(UUID_RE);
  });

  it('Telegram initData → that user, deterministic UUID', () => {
    const initData = new URLSearchParams({
      user: JSON.stringify({
        id: 123456,
        first_name: 'Рома',
        language_code: 'ru',
      }),
      auth_date: '1700000000',
      hash: 'x',
    }).toString();
    const u = userFromInitData(initData);
    expect(u).toMatchObject({
      telegramId: 123456,
      firstName: 'Рома',
      languageCode: 'ru',
    });
    expect(u.id).toBe(uuidFromSeed('tg:123456'));
    expect(u.id).toMatch(UUID_RE);
  });

  it('refresh rotates tokens; reused refresh token is rejected', async () => {
    const first = await authApi.login('');
    const second = await authApi.refresh(first.refreshToken);
    expect(second.accessToken).not.toBe(first.accessToken);
    await expect(authApi.refresh(first.refreshToken)).rejects.toMatchObject({
      status: 401,
    });
  });
});

describe('mock API contract', () => {
  it('cursor pagination walks the whole list without duplicates', async () => {
    const seen: string[] = [];
    let cursor: string | null = null;
    let pages = 0;
    do {
      const page = await tracksApi.list('popular', cursor);
      seen.push(...page.items.map((t) => t.id));
      cursor = page.nextCursor;
      pages++;
    } while (cursor && pages < 10);
    expect(pages).toBeGreaterThan(1);
    expect(new Set(seen).size).toBe(CATALOG.length);
  });

  it('paginate() clamps limit', () => {
    const p = paginate([1, 2, 3], null, '999');
    expect(p.items).toEqual([1, 2, 3]);
    expect(p.nextCursor).toBeNull();
  });

  it('requires auth', async () => {
    token = null;
    await expect(tracksApi.list('recent', null)).rejects.toMatchObject({
      status: 401,
    });
  });

  it('likes, playlists and positions round-trip', async () => {
    await tracksApi.like('t1');
    const liked = await tracksApi.list('liked', null);
    expect(liked.items.find((t) => t.id === 't1')?.liked).toBe(true);

    const p = await playlistsApi.create('  Новая  ');
    expect(p.title).toBe('Новая');
    await playlistsApi.addTrack(p.id, 't2');
    await playlistsApi.addTrack(p.id, 't2');
    expect((await playlistsApi.get(p.id)).tracks.map((t) => t.id)).toEqual(['t2']);
    await expect(playlistsApi.create('   ')).rejects.toMatchObject({
      status: 400,
    });

    await tracksApi.savePosition('t5', 61.26);
    expect((await tracksApi.getPosition('t5')).positionSec).toBeCloseTo(61.3);
  });

  it('search filters by title/artist/album', async () => {
    const r = await tracksApi.search('volt', null);
    expect(r.items.length).toBeGreaterThan(0);
    expect(r.items.every((t) => `${t.title} ${t.artist} ${t.album}`.toLowerCase().includes('volt'))).toBe(true);
  });
});
