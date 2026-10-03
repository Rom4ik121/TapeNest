import { describe, expect, it } from 'vitest';
import { ApiError } from './client';
import { errorMessageKey } from './errors';

describe('errorMessageKey', () => {
  it('distinguishes offline, not deployed (501), unavailable (503) and generic', () => {
    expect(errorMessageKey(new ApiError(0, 'network'))).toBe('error.network');
    expect(errorMessageKey(new ApiError(501, 'not implemented', 'NOT_IMPLEMENTED'))).toBe('error.notDeployed');
    expect(errorMessageKey(new ApiError(503, 'down', 'SERVICE_UNAVAILABLE'))).toBe('error.unavailable');
    expect(errorMessageKey(new ApiError(500, 'boom'))).toBe('error.generic');
    expect(errorMessageKey(new Error('x'))).toBe('error.generic');
  });

  it('treats 501 like 503 for "section unavailable" handling', () => {
    expect(new ApiError(501, 'x').isUnavailable).toBe(true);
    expect(new ApiError(503, 'x').isUnavailable).toBe(true);
    expect(new ApiError(0, 'x').isOffline).toBe(true);
    expect(new ApiError(401, 'x').isUnavailable).toBe(false);
  });
});
