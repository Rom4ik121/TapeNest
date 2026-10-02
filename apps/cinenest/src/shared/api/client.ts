import { config } from '@/shared/config';
import { createTimedSignal } from './timeout';
import type { ApiErrorBody } from './types';

export const API_PREFIX = '/api/v1';
const DEFAULT_TIMEOUT_MS = 20_000;

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
    public readonly code?: string,
    public readonly service?: string,
  ) {
    super(message);
    this.name = 'ApiError';
  }

  /** Section can't work right now: network down, upstream failing (503) or not deployed (501). */
  get isUnavailable(): boolean {
    return this.status === 503 || this.status === 501 || this.status === 0;
  }

  get isOffline(): boolean {
    return this.status === 0;
  }
}

/** Auth hooks injected by the auth layer (avoids a store ↔ client import cycle). */
export interface AuthBridge {
  getAccessToken: () => string | null;
  /** Obtain fresh tokens; resolve true on success. */
  refresh: () => Promise<boolean>;
}

let bridge: AuthBridge = {
  getAccessToken: () => null,
  refresh: async () => false,
};
export function setAuthBridge(next: AuthBridge): void {
  bridge = next;
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  body?: unknown;
  query?: Record<string, string | number | null | undefined>;
  /** false — do not send Authorization and do not refresh on 401 */
  auth?: boolean;
  signal?: AbortSignal;
  timeoutMs?: number;
  /** Keep the request alive while the page unloads (position save on pagehide). */
  keepalive?: boolean;
}

export function buildUrl(path: string, query?: RequestOptions['query']): string {
  let url = `${config.apiUrl}${API_PREFIX}${path}`;
  if (query) {
    const params = new URLSearchParams();
    for (const [k, v] of Object.entries(query)) {
      if (v !== undefined && v !== null && v !== '') params.set(k, String(v));
    }
    const qs = params.toString();
    if (qs) url += `${url.includes('?') ? '&' : '?'}${qs}`;
  }
  return url;
}

// Single in-flight refresh shared by all concurrent 401s.
let refreshInFlight: Promise<boolean> | null = null;
function refreshOnce(): Promise<boolean> {
  refreshInFlight ??= bridge.refresh().finally(() => {
    refreshInFlight = null;
  });
  return refreshInFlight;
}

async function send(path: string, options: RequestOptions): Promise<Response> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  // DEV through ngrok-free: never let the tunnel's HTML interstitial answer an API call.
  if (import.meta.env.DEV) headers['ngrok-skip-browser-warning'] = '1';
  if (options.body !== undefined) headers['Content-Type'] = 'application/json';
  const token = options.auth === false ? null : bridge.getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;

  const timed = createTimedSignal(options.timeoutMs ?? DEFAULT_TIMEOUT_MS, options.signal);
  try {
    return await fetch(buildUrl(path, options.query), {
      method: options.method ?? 'GET',
      headers,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: timed.signal,
      keepalive: options.keepalive,
    });
  } catch (err) {
    if (options.signal?.aborted) throw err; // caller cancelled — propagate AbortError
    throw new ApiError(0, timed.timedOut() ? 'timeout' : 'network', timed.timedOut() ? 'TIMEOUT' : 'NETWORK');
  } finally {
    timed.dispose();
  }
}

/**
 * Typed HTTP client: bearer JWT, timeout (old-WebView safe), one shared
 * refresh on 401 with a single retry, structured ApiError.
 */
export async function api<T>(path: string, options: RequestOptions = {}): Promise<T> {
  let res = await send(path, options);
  if (res.status === 401 && options.auth !== false) {
    if (await refreshOnce()) res = await send(path, options);
  }

  if (!res.ok) {
    let body: ApiErrorBody = {};
    try {
      body = (await res.json()) as ApiErrorBody;
    } catch {
      // not JSON
    }
    throw new ApiError(res.status, body.message ?? `HTTP ${res.status}`, body.code, body.service);
  }

  if (res.status === 204 || res.headers.get('content-length') === '0') return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}
