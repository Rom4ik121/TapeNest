import { describe, expect, it } from 'vitest';
import { identityPhoto } from './recipe';

describe('identityPhoto', () => {
  it('exports the whole frame as jpeg', () => {
    const r = identityPhoto();
    expect(r.crop).toEqual({ x: 0, y: 0, w: 1, h: 1 });
    expect(r.format).toBe('jpeg');
    expect(r.text.text).toBe('');
  });
});