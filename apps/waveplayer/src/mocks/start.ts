import { getResponse, type RequestHandler } from 'msw';
import { config, mockDomainOf } from '@/shared/config';
import { handlersFor } from './handlers';

export type MockMode = 'service-worker' | 'fetch-patch';

function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise((resolve, reject) => {
    const id = setTimeout(() => reject(new Error('timeout')), ms);
    p.then(
      (v) => (clearTimeout(id), resolve(v)),
      (e: unknown) => (clearTimeout(id), reject(e instanceof Error ? e : new Error(String(e)))),
    );
  });
}

/**
 * In-page fallback: route /api/v1/* through the same MSW handlers without a
 * Service Worker. Needed where SW is unavailable — notably iOS Telegram
 * (WKWebView without App-Bound Domains), private modes, or when a tunnel
 * interstitial breaks the SW script fetch.
 */
function patchFetch(handlers: RequestHandler[]): void {
  const original = window.fetch.bind(window);
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const target = typeof input === 'string' ? new URL(input, window.location.href) : input;
    const request = new Request(target, init);
    const domain = mockDomainOf(new URL(request.url, window.location.href).pathname);
    if (domain && config.isMocked(domain)) {
      const mocked = await getResponse(handlers, request);
      if (mocked) return mocked;
    }
    return original(input, init);
  };
}

/** A tunnel interstitial (ngrok free) may serve HTML instead of the SW script. */
async function isScriptServed(url: string): Promise<boolean> {
  try {
    const res = await fetch(url, { method: 'HEAD', cache: 'no-store' });
    return res.ok && /javascript/i.test(res.headers.get('content-type') ?? '');
  } catch {
    return false;
  }
}

/**
 * ngrok-free answers browser-initiated requests without the skip header (SW
 * registration can't send one) with an HTML interstitial → never try the SW there.
 */
const isNgrokFree = (host: string): boolean => /\.ngrok-free\.(app|dev)$/i.test(host);

export async function startMocks(): Promise<MockMode> {
  const handlers = handlersFor(config.mocks);
  const swUrl = `${import.meta.env.BASE_URL}mockServiceWorker.js`;
  if (
    'serviceWorker' in navigator &&
    window.isSecureContext &&
    !isNgrokFree(window.location.hostname) &&
    (await isScriptServed(swUrl))
  ) {
    try {
      const { createWorker } = await import('./browser');
      const worker = createWorker(handlers);
      await withTimeout(
        worker.start({
          onUnhandledRequest: 'bypass',
          quiet: true,
          serviceWorker: { url: swUrl },
        }),
        5000,
      );
      return 'service-worker';
    } catch {
      // fall through to the in-page patch
    }
  }
  patchFetch(handlers);
  return 'fetch-patch';
}
