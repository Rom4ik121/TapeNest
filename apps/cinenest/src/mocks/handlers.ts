import { delay, http, HttpResponse, type HttpResponseResolver } from 'msw';
import type { AuthTokens, Page, StreamSession, Title, User } from '@/shared/api/types';
import type { MockDomain } from '@/shared/config';
import { findTitle, mockDb, TITLES, type MockTitle } from './data';
import { uuidFromSeed } from './uuid';

/**
 * MSW handlers emulating api-gateway auth + streaming-service per
 * docs/api/cinema.openapi.yaml. Dev-only (VITE_USE_MOCKS), dynamically imported.
 */
const API = '*/api/v1';
const LATENCY = import.meta.env.MODE === 'test' ? 0 : 160;
/** Simulated TorrServer warm-up (spec §5.5). */
export const WARMUP_MS = import.meta.env.MODE === 'test' ? 50 : 4500;
/** Dev HLS stream served by the Vite dev plugin (vite.config.ts → mockHls). */
export const MOCK_HLS_URL = `${import.meta.env.BASE_URL}__mock-hls/index.m3u8`;
/** This title never finds peers — exercises the "stream failed" UI. */
export const FAILING_TITLE_ID = TITLES[16]!.id;

type TgUser = {
  id: number;
  first_name?: string;
  last_name?: string;
  username?: string;
  photo_url?: string;
  language_code?: string;
};

/** Mock /auth/telegram: real initData (inside Telegram) → that user; empty → guest. */
export function userFromInitData(initData: string): User {
  let tg: TgUser | null = null;
  try {
    const raw = new URLSearchParams(initData).get('user');
    if (raw) tg = JSON.parse(raw) as TgUser;
  } catch {
    tg = null;
  }
  if (!tg || typeof tg.id !== 'number') {
    return {
      id: uuidFromSeed('guest'),
      telegramId: 0,
      firstName: 'Guest',
      lastName: null,
      username: null,
      photoUrl: null,
      languageCode: null,
    };
  }
  return {
    id: uuidFromSeed(`tg:${tg.id}`),
    telegramId: tg.id,
    firstName: tg.first_name ?? 'User',
    lastName: tg.last_name ?? null,
    username: tg.username ?? null,
    photoUrl: tg.photo_url ?? null,
    languageCode: tg.language_code ?? null,
  };
}

const sessions = new Map<string, User>(); // refreshToken → user
const accessUsers = new Map<string, User>(); // accessToken → user
function issue(user: User): AuthTokens {
  const n = Math.random().toString(36).slice(2, 10);
  const refreshToken = `mock-refresh.${user.id}.${n}`;
  const accessToken = `mock-access.${user.id}.${n}`;
  sessions.set(refreshToken, user);
  accessUsers.set(accessToken, user);
  return { accessToken, refreshToken, expiresIn: 900, user };
}

/** Opaque cursor (mock). The real backend uses keyset cursors. */
export function paginate<T>(items: T[], cursor: string | null, limitRaw: string | null): Page<T> {
  const limit = Math.min(50, Math.max(1, Number(limitRaw) || 24));
  const start = cursor ? Number(atob(cursor).split(':')[1]) || 0 : 0;
  const slice = items.slice(start, start + limit);
  const end = start + slice.length;
  return { items: slice, nextCursor: end < items.length ? btoa(`o:${end}`) : null };
}

/** Real gateway auth → any Bearer passes (mock can't verify JWTs); mocked auth → only mock tokens. */
let strictTokens = true;

function authed(resolver: HttpResponseResolver): HttpResponseResolver {
  return async (info) => {
    await delay(LATENCY);
    const auth = info.request.headers.get('authorization') ?? '';
    if (strictTokens ? !auth.startsWith('Bearer mock-access.') : !/^Bearer \S+/.test(auth)) {
      return HttpResponse.json({ message: 'unauthorized', code: 'UNAUTHORIZED' }, { status: 401 });
    }
    return resolver(info);
  };
}

const notFound = (what: string) =>
  HttpResponse.json({ message: `${what} not found`, code: 'NOT_FOUND' }, { status: 404 });
const noContent = () => new HttpResponse(null, { status: 204 });

const summary = (t: MockTitle) => {
  const { description: _d, backdropUrl: _b, runtimeMin: _r, files: _f, ...rest } = t;
  return { ...rest, inWatchlist: mockDb.watchlist.has(t.id) };
};
const full = (t: MockTitle): Title => ({ ...t, inWatchlist: mockDb.watchlist.has(t.id) });

interface MockStream {
  id: string;
  titleId: string;
  fileId: string;
  startedAt: number;
}
const streams = new Map<string, MockStream>();

export function streamState(s: MockStream, now = Date.now()): StreamSession {
  const base = { id: s.id, titleId: s.titleId, fileId: s.fileId };
  if (s.titleId === FAILING_TITLE_ID) {
    const failed = now - s.startedAt > WARMUP_MS;
    return {
      ...base,
      status: failed ? 'failed' : 'warming',
      bufferedPct: 0,
      peers: 0,
      speedBps: 0,
      hlsUrl: null,
      error: failed ? 'no peers' : null,
    };
  }
  const elapsed = now - s.startedAt;
  if (elapsed >= WARMUP_MS) {
    return {
      ...base,
      status: 'ready',
      bufferedPct: 100,
      peers: 42,
      speedBps: 6.5e6,
      hlsUrl: MOCK_HLS_URL,
      error: null,
    };
  }
  const k = elapsed / WARMUP_MS;
  return {
    ...base,
    status: 'warming',
    bufferedPct: k < 0.2 ? 0 : Math.round(((k - 0.2) / 0.8) * 99),
    peers: k < 0.2 ? 0 : Math.round(8 + k * 30),
    speedBps: k < 0.2 ? 0 : Math.round(1.5e6 + k * 5e6),
    hlsUrl: null,
    error: null,
  };
}

export const authHandlers = [
  // ── auth (api-gateway/internal/auth) ───────────────────────────────
  http.post(`${API}/auth/telegram`, async ({ request }) => {
    await delay(LATENCY);
    const body = (await request.json().catch(() => ({}))) as {
      initData?: string;
    };
    return HttpResponse.json(issue(userFromInitData(body.initData ?? '')));
  }),
  http.post(`${API}/auth/refresh`, async ({ request }) => {
    await delay(LATENCY);
    const body = (await request.json().catch(() => ({}))) as {
      refreshToken?: string;
    };
    const user = body.refreshToken ? sessions.get(body.refreshToken) : undefined;
    if (!user || !body.refreshToken) {
      return HttpResponse.json({ message: 'invalid refresh token', code: 'UNAUTHORIZED' }, { status: 401 });
    }
    sessions.delete(body.refreshToken); // rotation
    return HttpResponse.json(issue(user));
  }),
  http.post(`${API}/auth/logout`, async ({ request }) => {
    const body = (await request.json().catch(() => ({}))) as {
      refreshToken?: string;
    };
    if (body.refreshToken) sessions.delete(body.refreshToken);
    return noContent();
  }),
  http.get(`${API}/me`, async ({ request }) => {
    await delay(LATENCY);
    const token = (request.headers.get('authorization') ?? '').replace(/^Bearer /, '');
    const user = accessUsers.get(token);
    return user
      ? HttpResponse.json(user)
      : HttpResponse.json({ message: 'unauthorized', code: 'UNAUTHORIZED' }, { status: 401 });
  }),
];

export const cinemaHandlers = [
  http.get(
    `${API}/cinema/titles`,
    authed(({ request }) => {
      const u = new URL(request.url);
      const q = (u.searchParams.get('q') ?? '').trim().toLowerCase();
      const kind = u.searchParams.get('kind');
      const items = TITLES.filter(
        (t) => (!kind || t.kind === kind) && (!q || `${t.title} ${t.originalTitle ?? ''}`.toLowerCase().includes(q)),
      ).map(summary);
      return HttpResponse.json(paginate(items, u.searchParams.get('cursor'), u.searchParams.get('limit')));
    }),
  ),
  http.get(
    `${API}/cinema/titles/:id`,
    authed(({ params }) => {
      const t = findTitle(String(params.id));
      return t ? HttpResponse.json(full(t)) : notFound('title');
    }),
  ),
  http.put(
    `${API}/cinema/titles/:id/watchlist`,
    authed(({ params }) => {
      const t = findTitle(String(params.id));
      if (!t) return notFound('title');
      mockDb.watchlist.add(t.id);
      return noContent();
    }),
  ),
  http.delete(
    `${API}/cinema/titles/:id/watchlist`,
    authed(({ params }) => {
      mockDb.watchlist.delete(String(params.id));
      return noContent();
    }),
  ),
  http.get(
    `${API}/cinema/watchlist`,
    authed(({ request }) => {
      const u = new URL(request.url);
      const items = TITLES.filter((t) => mockDb.watchlist.has(t.id)).map(summary);
      return HttpResponse.json(paginate(items, u.searchParams.get('cursor'), u.searchParams.get('limit')));
    }),
  ),
  http.get(
    `${API}/cinema/titles/:id/position`,
    authed(({ params, request }) => {
      const fileId = new URL(request.url).searchParams.get('fileId') ?? '';
      const p = mockDb.positions.get(`${String(params.id)}:${fileId}`);
      return p ? HttpResponse.json(p) : notFound('position');
    }),
  ),
  http.put(
    `${API}/cinema/titles/:id/position`,
    authed(async ({ params, request }) => {
      const b = (await request.json()) as { fileId?: string; positionSec?: number; durationSec?: number };
      const t = findTitle(String(params.id));
      if (!t || !b.fileId || !t.files.some((f) => f.id === b.fileId)) return notFound('file');
      if (typeof b.positionSec !== 'number' || b.positionSec < 0) {
        return HttpResponse.json({ message: 'positionSec invalid', code: 'INVALID' }, { status: 400 });
      }
      mockDb.positions.set(`${t.id}:${b.fileId}`, {
        titleId: t.id,
        fileId: b.fileId,
        positionSec: b.positionSec,
        durationSec: b.durationSec ?? 0,
        updatedAt: new Date().toISOString(),
      });
      return noContent();
    }),
  ),
  http.get(
    `${API}/cinema/continue`,
    authed(({ request }) => {
      const u = new URL(request.url);
      const items = [...mockDb.positions.values()]
        .filter((p) => p.positionSec >= 10 && (p.durationSec <= 0 || p.positionSec / p.durationSec <= 0.95))
        .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
        .flatMap((p) => {
          const t = findTitle(p.titleId);
          const f = t?.files.find((x) => x.id === p.fileId);
          return t && f ? [{ title: summary(t), file: f, position: p }] : [];
        });
      return HttpResponse.json(paginate(items, u.searchParams.get('cursor'), u.searchParams.get('limit')));
    }),
  ),
  http.post(
    `${API}/cinema/streams`,
    authed(async ({ request }) => {
      const b = (await request.json()) as { titleId?: string; fileId?: string };
      const t = b.titleId ? findTitle(b.titleId) : undefined;
      if (!t || !t.files.some((f) => f.id === b.fileId)) return notFound('file');
      // Same file → reuse the running stream (TorrServer keeps the torrent warm).
      const existing = [...streams.values()].find((s) => s.titleId === t.id && s.fileId === b.fileId);
      const s = existing ?? {
        id: uuidFromSeed(`stream:${t.id}:${b.fileId}:${Date.now()}`),
        titleId: t.id,
        fileId: b.fileId!,
        startedAt: Date.now(),
      };
      streams.set(s.id, s);
      return HttpResponse.json(streamState(s), { status: 201 });
    }),
  ),
  http.get(
    `${API}/cinema/streams/:id`,
    authed(({ params }) => {
      const s = streams.get(String(params.id));
      return s ? HttpResponse.json(streamState(s)) : notFound('stream');
    }),
  ),
  http.delete(
    `${API}/cinema/streams/:id`,
    authed(({ params }) => {
      streams.delete(String(params.id));
      return noContent();
    }),
  ),
];

/** Handlers for the mocked domains only; other requests pass through to the network. */
export function handlersFor(domains: ReadonlySet<MockDomain>) {
  strictTokens = domains.has('auth');
  return [...(domains.has('auth') ? authHandlers : []), ...(domains.has('cinema') ? cinemaHandlers : [])];
}

export const handlers = [...authHandlers, ...cinemaHandlers];

/** Test helper. */
export function __resetStreams(): void {
  streams.clear();
}
