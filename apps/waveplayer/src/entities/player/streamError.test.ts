import { describe, expect, it } from 'vitest';
import { ApiError } from '@/shared/api/client';
import { streamErrorKey } from '.';

describe('streamErrorKey', () => {
  it.each([
    ['NO_SOURCES', 'player.noSources'],
    ['ACQUIRE_QUOTA', 'player.quota'],
    ['NOT_AVAILABLE', 'player.notAvailable'],
    ['ACQUIRE_TIMEOUT', 'player.acquireTimeout'],
  ])('%s → %s', (code, key) => {
    expect(streamErrorKey(new ApiError(404, 'x', code))).toBe(key);
  });

  it('stays silent for other failures', () => {
    expect(streamErrorKey(new ApiError(503, 'x', 'UNAVAILABLE'))).toBeNull();
    expect(streamErrorKey(new Error('boom'))).toBeNull();
  });
});
