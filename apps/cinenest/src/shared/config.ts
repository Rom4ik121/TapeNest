/**
 * Build-time configuration (Vite env).
 *
 * VITE_USE_MOCKS is per API domain (dev only, MSW):
 *   "cinema" (dev default) — auth goes to the real api-gateway, /cinema/* is mocked
 *                            until streaming-service exists (stage 4; the gateway answers 501);
 *   "true" | "all"         — everything mocked (fully offline frontend work);
 *   "false" | "none"       — no mocks (production);
 *   "auth,cinema"          — explicit comma list.
 * Unset in a production build → no mocks.
 */
export type MockDomain = 'auth' | 'cinema';
const ALL: readonly MockDomain[] = ['auth', 'cinema'];

export function parseMockDomains(raw: string | undefined, isDev = true): ReadonlySet<MockDomain> {
  const v = (raw ?? '').trim().toLowerCase();
  if (v === '') return new Set<MockDomain>(isDev ? ['cinema'] : []);
  if (v === 'true' || v === 'all' || v === '1') return new Set(ALL);
  if (v === 'false' || v === 'none' || v === '0') return new Set();
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
  if (/^\/cinema(\/|$)/.test(p)) return 'cinema';
  return null;
}

const mocks = parseMockDomains(import.meta.env.VITE_USE_MOCKS, import.meta.env.DEV);

export const config = {
  /** api-gateway origin without /api/v1. Empty = same origin (Vite proxy / nginx). */
  apiUrl: (import.meta.env.VITE_API_URL ?? '').trim().replace(/\/+$/, ''),
  mocks,
  /** Bot username (without @) for the "open in Telegram" link; optional. */
  botUsername: (import.meta.env.VITE_BOT_USERNAME ?? '').trim().replace(/^@/, ''),
  /** Any mocks at all → MSW is started. */
  useMocks: mocks.size > 0,
  isMocked: (d: MockDomain): boolean => mocks.has(d),
} as const;
