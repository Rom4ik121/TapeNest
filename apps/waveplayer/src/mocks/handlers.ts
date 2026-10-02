import { delay, http, HttpResponse, type HttpResponseResolver } from 'msw';
import type { AuthTokens, Page, Track, User } from '@/shared/api/types';
import type { MockDomain } from '@/shared/config';
import { albumId, artistId, CATALOG, findTrack, mockAlbums, mockArtists, mockDb, toPlaylist, withLiked } from './data';
import { toneUrl } from './tone';
import { uuidFromSeed } from './uuid';

/**
 * MSW handlers emulating api-gateway + music-service per
 * docs/api/waveplayer-contract.md. Dev-only (VITE_USE_MOCKS); loaded through a
 * dynamic import so they never land in a production bundle without the flag.
 */
const API = '*/api/v1';
const LATENCY = import.meta.env.MODE === 'test' ? 0 : 180;

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

/**
 * When auth is mocked too, music handlers accept only mock tokens (so the
 * 401 → refresh path is exercised); with the real gateway any Bearer JWT is
 * accepted — the mock cannot verify gateway signatures (dev only).
 */
let strictTokens = true;

/** Opaque cursor (mock). The real backend uses keyset cursors. */
export function paginate<T>(items: T[], cursor: string | null, limitRaw: string | null): Page<T> {
  const limit = Math.min(100, Math.max(1, Number(limitRaw) || 20));
  const start = cursor ? Number(atob(cursor).split(':')[1]) || 0 : 0;
  const slice = items.slice(start, start + limit);
  const end = start + slice.length;
  return {
    items: slice,
    nextCursor: end < items.length ? btoa(`o:${end}`) : null,
  };
}

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

function listResponse(request: Request, items: Array<Omit<Track, 'liked'>>) {
  const url = new URL(request.url);
  return HttpResponse.json(
    paginate(items.map(withLiked), url.searchParams.get('cursor'), url.searchParams.get('limit')),
  );
}

// Stable-per-session shuffles so pagination doesn't repeat tracks.
function seededShuffle<T>(arr: readonly T[], seed: number): T[] {
  const a = [...arr];
  let s = seed || 1;
  for (let i = a.length - 1; i > 0; i--) {
    s = (Math.imul(s, 1103515245) + 12345) & 0x7fffffff;
    const j = s % (i + 1);
    [a[i], a[j]] = [a[j] as T, a[i] as T];
  }
  return a;
}
const POPULAR = seededShuffle(CATALOG, 42);
const RECENT = seededShuffle(CATALOG, 7).slice(0, 12);

const waveSessions = new Map<string, number>();

/** Rotating explanations so the mock wave shows every caption style. */
function mockReason(i: number, ref?: { id: string; title: string; artist: string }): Record<string, string> {
  const kinds = ['because_you_liked', 'genre_you_like', 'similar_listeners', 'mood_you_like', 'discovery', 'popular'];
  const kind = kinds[i % kinds.length] ?? 'discovery';
  if (kind === 'because_you_liked') {
    return ref ? { kind, refTrackId: ref.id, refTitle: ref.title, refArtist: ref.artist } : { kind: 'popular' };
  }
  if (kind === 'genre_you_like') return { kind, genre: 'Jazz' };
  if (kind === 'mood_you_like') return { kind, tag: 'calm' };
  return { kind };
}
let waveCounter = 0;

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

export const musicHandlers = [
  // ── tracks ─────────────────────────────────────────────────────────
  http.get(
    `${API}/tracks/recent`,
    authed(({ request }) => listResponse(request, RECENT)),
  ),
  http.get(
    `${API}/tracks/popular`,
    authed(({ request }) => listResponse(request, POPULAR)),
  ),
  http.get(
    `${API}/tracks/liked`,
    authed(({ request }) =>
      listResponse(
        request,
        CATALOG.filter((t) => mockDb.liked.has(t.id)),
      ),
    ),
  ),
  http.get(
    `${API}/tracks/search`,
    authed(({ request }) => {
      const q = (new URL(request.url).searchParams.get('q') ?? '').trim().toLowerCase();
      const items = q ? CATALOG.filter((t) => `${t.title} ${t.artist} ${t.album ?? ''}`.toLowerCase().includes(q)) : [];
      return listResponse(request, items);
    }),
  ),
  // ── unified catalog (ADR 0011) ─────────────────────────────────────
  http.get(
    `${API}/search`,
    authed(({ request }) => {
      const q = (new URL(request.url).searchParams.get('q') ?? '').trim().toLowerCase();
      if (!q) return HttpResponse.json({ message: 'q required', code: 'VALIDATION' }, { status: 400 });
      const tracks = CATALOG.filter((t) => `${t.title} ${t.artist} ${t.album ?? ''}`.toLowerCase().includes(q));
      return HttpResponse.json({
        tracks: tracks.slice(0, 20).map(withLiked),
        albums: mockAlbums().filter((a) => `${a.title} ${a.artist}`.toLowerCase().includes(q)),
        artists: mockArtists().filter((a) => a.name.toLowerCase().includes(q)),
      });
    }),
  ),
  http.get(
    `${API}/albums/:id`,
    authed(({ params }) => {
      const album = mockAlbums().find((a) => a.id === params.id);
      if (!album) return notFound('album');
      return HttpResponse.json({ album, tracks: CATALOG.filter((t) => albumId(t.album) === album.id).map(withLiked) });
    }),
  ),
  http.get(
    `${API}/artists/:id`,
    authed(({ params }) => {
      const artist = mockArtists().find((a) => a.id === params.id);
      if (!artist) return notFound('artist');
      return HttpResponse.json({
        artist,
        albums: mockAlbums().filter((a) => a.artistId === artist.id),
        tracks: CATALOG.filter((t) => artistId(t.artist) === artist.id).map(withLiked),
      });
    }),
  ),
  http.get(
    `${API}/tracks/:id/stream-url`,
    authed(({ params }) => {
      const t = findTrack(String(params.id));
      if (!t) return notFound('track');
      // "remote" mock tracks are fetched first: one 202 round, then playable
      if (t.remote && !mockDb.acquired.has(t.id)) {
        mockDb.acquired.add(t.id);
        return HttpResponse.json({ state: 'downloading', progress: 0.1, retryAfterMs: 800 }, { status: 202 });
      }
      return HttpResponse.json({
        url: toneUrl(t.id, t.durationSec),
        expiresAt: new Date(Date.now() + 3_600_000).toISOString(),
      });
    }),
  ),
  http.post(
    `${API}/tracks/:id/like`,
    authed(({ params }) => {
      mockDb.liked.add(String(params.id));
      return noContent();
    }),
  ),
  http.delete(
    `${API}/tracks/:id/like`,
    authed(({ params }) => {
      mockDb.liked.delete(String(params.id));
      return noContent();
    }),
  ),
  http.get(
    `${API}/tracks/:id/position`,
    authed(({ params }) => {
      const p = mockDb.positions.get(String(params.id));
      if (!p) return notFound('position');
      return HttpResponse.json({ trackId: String(params.id), ...p });
    }),
  ),
  http.put(
    `${API}/tracks/:id/position`,
    authed(async ({ params, request }) => {
      const body = (await request.json().catch(() => ({}))) as {
        positionSec?: number;
      };
      if (typeof body.positionSec !== 'number' || body.positionSec < 0) {
        return HttpResponse.json({ message: 'positionSec required', code: 'INVALID' }, { status: 400 });
      }
      mockDb.positions.set(String(params.id), {
        positionSec: body.positionSec,
        updatedAt: new Date().toISOString(),
      });
      return noContent();
    }),
  ),

  // ── playlists ──────────────────────────────────────────────────────
  http.get(
    `${API}/playlists`,
    authed(() => HttpResponse.json(mockDb.playlists.map(toPlaylist))),
  ),
  http.post(
    `${API}/playlists`,
    authed(async ({ request }) => {
      const body = (await request.json().catch(() => ({}))) as {
        title?: string;
      };
      const title = (body.title ?? '').trim().slice(0, 100);
      if (!title) return HttpResponse.json({ message: 'title required', code: 'INVALID' }, { status: 400 });
      const p = {
        id: `p${Date.now().toString(36)}`,
        title,
        trackIds: [],
        createdAt: new Date().toISOString(),
      };
      mockDb.playlists.push(p);
      return HttpResponse.json(toPlaylist(p));
    }),
  ),
  http.get(
    `${API}/playlists/:id`,
    authed(({ params }) => {
      const p = mockDb.playlists.find((x) => x.id === params.id);
      if (!p) return notFound('playlist');
      const tracks = p.trackIds
        .map(findTrack)
        .filter((t): t is Omit<Track, 'liked'> => !!t)
        .map(withLiked);
      return HttpResponse.json({ playlist: toPlaylist(p), tracks });
    }),
  ),
  http.patch(
    `${API}/playlists/:id`,
    authed(async ({ params, request }) => {
      const p = mockDb.playlists.find((x) => x.id === params.id);
      if (!p) return notFound('playlist');
      const body = (await request.json().catch(() => ({}))) as {
        title?: string;
      };
      const title = (body.title ?? '').trim().slice(0, 100);
      if (!title) return HttpResponse.json({ message: 'title required', code: 'INVALID' }, { status: 400 });
      p.title = title;
      return HttpResponse.json(toPlaylist(p));
    }),
  ),
  http.delete(
    `${API}/playlists/:id`,
    authed(({ params }) => {
      mockDb.playlists = mockDb.playlists.filter((x) => x.id !== params.id);
      return noContent();
    }),
  ),
  http.post(
    `${API}/playlists/:id/tracks`,
    authed(async ({ params, request }) => {
      const p = mockDb.playlists.find((x) => x.id === params.id);
      if (!p) return notFound('playlist');
      const body = (await request.json().catch(() => ({}))) as {
        trackId?: string;
      };
      if (!body.trackId || !findTrack(body.trackId)) return notFound('track');
      if (!p.trackIds.includes(body.trackId)) p.trackIds.push(body.trackId);
      return noContent();
    }),
  ),
  http.delete(
    `${API}/playlists/:id/tracks/:trackId`,
    authed(({ params }) => {
      const p = mockDb.playlists.find((x) => x.id === params.id);
      if (p) p.trackIds = p.trackIds.filter((t) => t !== params.trackId);
      return noContent();
    }),
  ),

  // ── wave ───────────────────────────────────────────────────────────
  http.post(
    `${API}/wave/sessions`,
    authed(async ({ request }) => {
      const body = (await request.json().catch(() => ({}))) as { mode?: string };
      const mode = body.mode ?? 'default';
      const sessionId = `ws-${++waveCounter}-${Date.now().toString(36)}`;
      const seed = Date.now() % 100000;
      waveSessions.set(sessionId, seed);
      // Heuristic MVP: liked first, then shuffled catalog.
      const liked = CATALOG.filter((t) => mockDb.liked.has(t.id));
      const rest = seededShuffle(
        CATALOG.filter((t) => !mockDb.liked.has(t.id)),
        seed,
      );
      const favs = seededShuffle(liked, seed).slice(0, 2);
      return HttpResponse.json({
        sessionId,
        strategy: 'reco',
        mode,
        tracks: [
          ...favs.map((t) => ({ ...withLiked(t), reason: { kind: 'favorite' } })),
          ...rest.slice(0, 10 - favs.length).map((t, i) => ({ ...withLiked(t), reason: mockReason(i, favs[0]) })),
        ],
      });
    }),
  ),
  http.get(
    `${API}/wave/sessions/:id/tracks`,
    authed(({ params, request }) => {
      const seed = waveSessions.get(String(params.id));
      if (seed === undefined) return notFound('wave session');
      const after = new URL(request.url).searchParams.get('after') ?? '';
      const next = (seed + after.length * 7919 + Date.now()) % 100000;
      return HttpResponse.json(
        seededShuffle(
          CATALOG.filter((t) => t.id !== after),
          next,
        )
          .slice(0, 6)
          .map((t, i) => ({ ...withLiked(t), reason: mockReason(i + 1) })),
      );
    }),
  ),
  http.post(
    `${API}/wave/sessions/:id/feedback`,
    authed(() => noContent()),
  ),

  // ── events ─────────────────────────────────────────────────────────
  http.post(
    `${API}/events/track-listened`,
    authed(() => noContent()),
  ),
  http.post(
    `${API}/events/track-skipped`,
    authed(() => noContent()),
  ),
];

/** Handlers for the mocked domains only; other requests pass through to the network. */
export function handlersFor(domains: ReadonlySet<MockDomain>) {
  strictTokens = domains.has('auth');
  return [...(domains.has('auth') ? authHandlers : []), ...(domains.has('music') ? musicHandlers : [])];
}

/** Every handler (tests). */
export const handlers = [...authHandlers, ...musicHandlers];
