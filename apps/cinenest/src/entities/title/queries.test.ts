import { describe, expect, it } from 'vitest';
import { flattenPages } from './queries';

describe('flattenPages', () => {
  it('concatenates pages and de-duplicates by id (keyset overlap)', () => {
    const out = flattenPages([
      { items: [{ id: 'a' }, { id: 'b' }], nextCursor: 'x' },
      { items: [{ id: 'b' }, { id: 'c' }], nextCursor: null },
    ]);
    expect(out.map((x) => x.id)).toEqual(['a', 'b', 'c']);
  });
  it('undefined → empty', () => expect(flattenPages(undefined)).toEqual([]));
});
