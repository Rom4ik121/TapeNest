import { describe, expect, it } from 'vitest';
import { track } from '../../../test/fixtures';
import { clearSession, loadSession, saveSession } from './session';

describe('player session storage', () => {
  it('round-trips and windows long queues around the current index', () => {
    const q = Array.from({ length: 300 }, (_, i) => track(`t${i}`));
    saveSession(q, 250, 12);
    const s = loadSession();
    expect(s).not.toBeNull();
    expect(s!.queue.length).toBeLessThanOrEqual(100);
    expect(s!.queue[s!.index]?.id).toBe('t250');
    expect(s!.positionSec).toBe(12);
    clearSession();
    expect(loadSession()).toBeNull();
  });

  it('rejects corrupted data', () => {
    localStorage.setItem('waveplayer:session:v1', '{"queue":[],"index":3}');
    expect(loadSession()).toBeNull();
    localStorage.setItem('waveplayer:session:v1', 'not json');
    expect(loadSession()).toBeNull();
  });
});
