import { describe, expect, it } from 'vitest';
import { mockDomainOf, parseMockDomains } from './config';

describe('VITE_USE_MOCKS per domain', () => {
  const p = (v: string | undefined) => [...parseMockDomains(v)].sort();

  it('defaults to no mocks since stage 3 (real music-service)', () => {
    expect(p(undefined)).toEqual([]);
    expect(p('')).toEqual([]);
    expect(p('  ')).toEqual([]);
  });

  it('keeps music-only mocks available on request', () => {
    expect(p('music')).toEqual(['music']);
  });

  it('production builds default to no mocks', () => {
    expect([...parseMockDomains(undefined, false)]).toEqual([]);
    expect([...parseMockDomains('all', false)].sort()).toEqual(['auth', 'music']);
  });

  it('accepts all/none aliases and legacy booleans', () => {
    expect(p('true')).toEqual(['auth', 'music']);
    expect(p('ALL')).toEqual(['auth', 'music']);
    expect(p('false')).toEqual([]);
    expect(p('none')).toEqual([]);
  });

  it('parses explicit lists and ignores unknown domains', () => {
    expect(p('auth, music')).toEqual(['auth', 'music']);
    expect(p('auth,video')).toEqual(['auth']);
    expect(p('video')).toEqual([]);
  });
});

describe('mockDomainOf', () => {
  it('maps auth endpoints and /me to "auth"', () => {
    expect(mockDomainOf('/api/v1/auth/telegram')).toBe('auth');
    expect(mockDomainOf('/api/v1/auth/refresh')).toBe('auth');
    expect(mockDomainOf('/api/v1/me')).toBe('auth');
  });

  it('maps music endpoints to "music"', () => {
    for (const path of [
      '/api/v1/tracks/popular',
      '/api/v1/tracks/t1/position',
      '/api/v1/playlists',
      '/api/v1/wave/sessions',
      '/api/v1/events/track-listened',
    ]) {
      expect(mockDomainOf(path)).toBe('music');
    }
  });

  it('returns null for other services and non-API paths', () => {
    expect(mockDomainOf('/api/v1/downloads')).toBeNull();
    expect(mockDomainOf('/api/v1/tracksXYZ')).toBeNull();
    expect(mockDomainOf('/assets/app.js')).toBeNull();
  });
});
