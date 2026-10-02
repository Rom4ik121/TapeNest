// @vitest-environment node
// (Node env: jsdom's AbortSignal is rejected by Node's fetch used by msw/node.)
import { setupServer } from 'msw/node';
import { afterAll, beforeAll, beforeEach, describe, expect, it } from 'vitest';
import { setAuthBridge } from '@/shared/api/client';
import { authApi, cinemaApi } from '@/shared/api/endpoints';
import { mockDb, resetMockDb, TITLES } from './data';
import {
  __resetStreams,
  FAILING_TITLE_ID,
  handlers,
  handlersFor,
  MOCK_HLS_URL,
  paginate,
  streamState,
  userFromInitData,
  WARMUP_MS,
} from './handlers';
import { UUID_RE } from './uuid';

const server = setupServer(...handlers);
let token: string | null = null;
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterAll(() => server.close());
beforeEach(async () => {
  resetMockDb();
  __resetStreams();
  setAuthBridge({ getAccessToken: () => token, refresh: async () => false });
  token = (await authApi.login('')).accessToken;
});

describe('mock auth', () => {
  it('guest user has a UUID id', () => {
    expect(userFromInitData('').id).toMatch(UUID_RE);
  });
  it('requests without a mock token are rejected (401)', async () => {
    token = 'garbage';
    await expect(cinemaApi.title(TITLES[0]!.id)).rejects.toMatchObject({ status: 401 });
  });
  it('handlersFor selects domains', () => {
    expect(handlersFor(new Set(['cinema'])).length).toBeGreaterThan(5);
    expect(handlersFor(new Set())).toHaveLength(0);
    handlersFor(new Set(['auth', 'cinema']));
  });
});

describe('catalog', () => {
  it('paginates with an opaque cursor', async () => {
    const p1 = await cinemaApi.titles({ q: '', kind: null }, null);
    expect(p1.items).toHaveLength(24);
    expect(p1.nextCursor).not.toBeNull();
    const p2 = await cinemaApi.titles({ q: '', kind: null }, p1.nextCursor);
    expect(p2.items).toHaveLength(TITLES.length - 24);
    expect(p2.nextCursor).toBeNull();
  });
  it('filters by kind and query', async () => {
    const series = await cinemaApi.titles({ q: '', kind: 'series' }, null);
    expect(series.items.every((t) => t.kind === 'series')).toBe(true);
    const q = TITLES[3]!.title.slice(0, 5);
    const found = await cinemaApi.titles({ q, kind: null }, null);
    expect(found.items.map((t) => t.id)).toContain(TITLES[3]!.id);
  });
  it('title details and 404', async () => {
    const t = await cinemaApi.title(TITLES[1]!.id);
    expect(t.files.length).toBeGreaterThan(0);
    await expect(cinemaApi.title('nope')).rejects.toMatchObject({ status: 404 });
  });
  it('paginate clamps limit', () => {
    expect(paginate([1, 2, 3], null, '999').items).toHaveLength(3);
    expect(paginate([1, 2, 3], null, '-5').items).toHaveLength(1);
  });
});

describe('watchlist', () => {
  it('add / list / remove', async () => {
    const id = TITLES[2]!.id;
    await cinemaApi.setWatchlist(id, true);
    expect((await cinemaApi.watchlist(null)).items.map((t) => t.id)).toContain(id);
    expect((await cinemaApi.title(id)).inWatchlist).toBe(true);
    await cinemaApi.setWatchlist(id, false);
    expect(mockDb.watchlist.has(id)).toBe(false);
  });
});

describe('positions & continue watching', () => {
  it('seeded continue item, 404 for unknown position', async () => {
    const c = await cinemaApi.continueWatching();
    expect(c.items.length).toBeGreaterThanOrEqual(1);
    await expect(cinemaApi.getPosition(TITLES[5]!.id, 'x')).rejects.toMatchObject({ status: 404 });
  });
  it('save → read → appears first in continue', async () => {
    const t = TITLES[6]!;
    const f = t.files[0]!;
    await cinemaApi.savePosition(t.id, f.id, 120.4, 3000);
    expect((await cinemaApi.getPosition(t.id, f.id)).positionSec).toBe(120);
    expect((await cinemaApi.continueWatching()).items[0]!.title.id).toBe(t.id);
  });
  it('rejects unknown files', async () => {
    await expect(cinemaApi.savePosition(TITLES[6]!.id, 'nope', 10, 100)).rejects.toMatchObject({ status: 404 });
  });
});

describe('streams', () => {
  it('warms up then becomes ready with the mock HLS url; DELETE stops it', async () => {
    const t = TITLES[0]!;
    const s = await cinemaApi.startStream(t.id, t.files[0]!.id);
    expect(s.status).toBe('warming');
    expect(s.hlsUrl).toBeNull();
    const again = await cinemaApi.startStream(t.id, t.files[0]!.id);
    expect(again.id).toBe(s.id);
    await sleep(WARMUP_MS + 30);
    const ready = await cinemaApi.stream(s.id);
    expect(ready).toMatchObject({ status: 'ready', hlsUrl: MOCK_HLS_URL, bufferedPct: 100 });
    await cinemaApi.stopStream(s.id);
    await expect(cinemaApi.stream(s.id)).rejects.toMatchObject({ status: 404 });
  });
  it('a title without peers fails', async () => {
    const t = TITLES.find((x) => x.id === FAILING_TITLE_ID)!;
    const s = await cinemaApi.startStream(t.id, t.files[0]!.id);
    await sleep(WARMUP_MS + 30);
    expect(await cinemaApi.stream(s.id)).toMatchObject({ status: 'failed', error: 'no peers' });
  });
  it('unknown file → 404', async () => {
    await expect(cinemaApi.startStream(TITLES[0]!.id, 'nope')).rejects.toMatchObject({ status: 404 });
  });
  it('streamState progresses monotonically', () => {
    const base = { id: 's', titleId: TITLES[0]!.id, fileId: 'f', startedAt: 0 };
    const a = streamState(base, WARMUP_MS * 0.1);
    const b = streamState(base, WARMUP_MS * 0.6);
    expect(a.bufferedPct).toBe(0);
    expect(b.bufferedPct).toBeGreaterThan(0);
    expect(b.peers).toBeGreaterThan(0);
  });
});
