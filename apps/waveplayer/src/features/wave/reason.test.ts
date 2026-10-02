import { describe, expect, it } from 'vitest';
import { i18n } from '@/i18n';
import { reasonText } from './reason';

describe('reasonText', () => {
  const t = i18n.getFixedT('en');
  const base = { artist: 'Chopin' };

  it('renders every reason kind', () => {
    expect(reasonText(t, { ...base, reason: { kind: 'because_you_liked', refTitle: 'Nocturne' } })).toBe(
      'Because you liked “Nocturne”',
    );
    expect(reasonText(t, { ...base, reason: { kind: 'artist_you_like', artist: 'Bach' } })).toBe('You listen to Bach');
    expect(reasonText(t, { ...base, reason: { kind: 'session_artist' } })).toBe('More Chopin — you liked it');
    expect(reasonText(t, { ...base, reason: { kind: 'genre_you_like', genre: 'Baroque' } })).toBe(
      'Your genre: Baroque',
    );
    expect(reasonText(t, { ...base, reason: { kind: 'mood_you_like', tag: 'calm' } })).toBe('Your mood: calm');
    expect(reasonText(t, { ...base, reason: { kind: 'mood_you_like', tag: 'weird' } })).toBe('Your mood: weird');
    for (const kind of ['similar_listeners', 'popular', 'new_in_catalog', 'discovery', 'favorite']) {
      expect(reasonText(t, { ...base, reason: { kind } })).toBeTruthy();
    }
  });

  it('is null without a usable reason', () => {
    expect(reasonText(t, base)).toBeNull();
    expect(reasonText(t, { ...base, reason: null })).toBeNull();
    expect(reasonText(t, { ...base, reason: { kind: 'genre_you_like' } })).toBeNull();
    expect(reasonText(t, { ...base, reason: { kind: 'unknown_future_kind' } })).toBeNull();
    expect(reasonText(t, { ...base, reason: { kind: 'because_you_liked' } })).toBe('A discovery for you');
  });

  it('has Russian captions', () => {
    const ru = i18n.getFixedT('ru');
    expect(reasonText(ru, { ...base, reason: { kind: 'because_you_liked', refTitle: 'Ноктюрн' } })).toBe(
      'Потому что вам нравится «Ноктюрн»',
    );
    expect(reasonText(ru, { ...base, reason: { kind: 'mood_you_like', tag: 'minor' } })).toBe(
      'Ваше настроение: минорное',
    );
  });
});
