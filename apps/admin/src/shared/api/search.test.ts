import { beforeEach, expect, it, vi } from 'vitest';
import { setAccessToken } from '@/shared/api/client';
import { searchCatalog } from '@/shared/api/search';

const calls: string[] = [];

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

beforeEach(() => {
  calls.length = 0;
  setAccessToken('token');
});

it('uses unified catalog search when it is available', async () => {
  vi.stubGlobal('fetch', async (input: RequestInfo | URL) => {
    const url = String(input);
    calls.push(url);
    return json({ tracks: [{ id: '1', title: 'A', artist: 'B', durationSec: 1 }] });
  });
  const tracks = await searchCatalog('a');
  expect(tracks.map((track) => track.title)).toEqual(['A']);
  expect(calls.some((url) => url.includes('/tracks/search'))).toBe(false);
});

it('falls back to the library search when unified search is off', async () => {
  vi.stubGlobal('fetch', async (input: RequestInfo | URL) => {
    const url = String(input);
    calls.push(url);
    if (url.includes('/tracks/search')) return json({ items: [{ id: '2', title: 'B', artist: 'C', durationSec: 2 }] });
    return json({ code: 'NOT_FOUND', message: 'not found' }, 404);
  });
  const tracks = await searchCatalog('b');
  expect(tracks.map((track) => track.title)).toEqual(['B']);
  expect(calls.some((url) => url.includes('/tracks/search'))).toBe(true);
});
