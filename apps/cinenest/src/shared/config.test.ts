import { describe, expect, it } from 'vitest';
import { mockDomainOf, parseMockDomains } from './config';

describe('VITE_USE_MOCKS per domain', () => {
  const p = (v: string | undefined, dev = true) => [...parseMockDomains(v, dev)].sort();

  it('dev default mocks only cinema (real auth); prod default mocks nothing', () => {
    expect(p(undefined)).toEqual(['cinema']);
    expect(p('', false)).toEqual([]);
  });

  it('accepts all/none aliases and explicit lists', () => {
    expect(p('all')).toEqual(['auth', 'cinema']);
    expect(p('true', false)).toEqual(['auth', 'cinema']);
    expect(p('none')).toEqual([]);
    expect(p('auth, music')).toEqual(['auth']);
  });
});

describe('mockDomainOf', () => {
  it('classifies paths', () => {
    expect(mockDomainOf('/api/v1/auth/refresh')).toBe('auth');
    expect(mockDomainOf('/api/v1/me')).toBe('auth');
    expect(mockDomainOf('/api/v1/cinema/titles')).toBe('cinema');
    expect(mockDomainOf('/api/v1/cinema/streams/x')).toBe('cinema');
    expect(mockDomainOf('/api/v1/tracks/popular')).toBeNull();
    expect(mockDomainOf('/cinenest/__mock-hls/index.m3u8')).toBeNull();
  });
});
