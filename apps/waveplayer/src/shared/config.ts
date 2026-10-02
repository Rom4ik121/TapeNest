/**
 * Build-time configuration (Vite env).
 *
 * VITE_USE_MOCKS is per API domain (dev only, MSW):
 *   unset | "none" | "false" — no mocks: real api-gateway + music-service
 *                       (dev default since stage 3 and always in production);
 *   "music"           — real auth, mocked catalog/playlists/wave/events (frontend
 *                       work without music-service/Navidrome);
 *   "true" | "all"    — everything mocked (fully offline frontend work);
 *   "auth,music"      — explicit comma list.
 * An explicit value also applies to production builds (mocked demo builds).
 * Unit tests use the MSW handlers directly, independent of this flag.
 */
export type MockDomain = 'auth' | 'music';
const ALL: readonly MockDomain[] = ['auth', 'music'];

// isDev is kept for call-site compatibility: since stage 3 the unset default is
// "no mocks" in both modes; an explicit value (e.g. a mocked demo build) wins.
export function parseMockDomains(raw: string | undefined, _isDev = true): ReadonlySet<MockDomain> {
  const v = (raw ?? '').trim().toLowerCase();
  // Unset → no mocks (dev and production; MSW is tree-shaken from production builds).
  if (v === '' || v === 'false' || v === 'none' || v === '0') return new Set();
  if (v === 'true' || v === 'all' || v === '1') return new Set(ALL);
  const out = new Set<MockDomain>();
  for (const part of v.split(',')) {
    const d = part.trim();
    if ((ALL as readonly string[]).includes(d)) out.add(d as MockDomain);
  }
  return out;
}

/** Which mock domain a request path (/api/v1/...) belongs to. */
export function mockDomainOf(pathname: string): MockDomain | null {
  const p = pathname.replace(/^.*?\/api\/v1/, '');
  if (p.startsWith('/auth/') || p === '/me') return 'auth';
  if (/^\/(tracks|playlists|wave|events)(\/|$)/.test(p)) return 'music';
  return null;
}

const mocks = parseMockDomains(import.meta.env.VITE_USE_MOCKS, import.meta.env.DEV);

export const config = {
  /**
   * api-gateway origin without /api/v1. Empty = same origin (dev: Vite proxies
   * /api to the gateway; prod: nginx does), which also works through ngrok.
   */
  apiUrl: (import.meta.env.VITE_API_URL ?? '').trim().replace(/\/+$/, ''),
  mocks,
  /** Bot username (without @) for the "open in Telegram" link; optional. */
  botUsername: (import.meta.env.VITE_BOT_USERNAME ?? '').trim().replace(/^@/, ''),
  /** Any mocks at all → MSW is started. */
  useMocks: mocks.size > 0,
  isMocked: (d: MockDomain): boolean => mocks.has(d),
} as const;
