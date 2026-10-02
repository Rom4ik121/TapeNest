import { describe, expect, it } from 'vitest';
import type { User } from '@/shared/api/types';
import { identityOf, isStoredSessionValid } from './session';

const user = (telegramId: number): User => ({
  id: '3f0c1d2e-8a4b-4c5d-9e6f-0123456789ab',
  telegramId,
  firstName: 'A',
  lastName: null,
  username: null,
  photoUrl: null,
  languageCode: 'ru',
});

describe('stored JWT ↔ current Telegram user', () => {
  const tgUser = (id: number) => ({
    user: { id, firstName: 'A', languageCode: 'ru' },
  });

  it('keeps tokens of the same Telegram user', () => {
    expect(isStoredSessionValid({ accessToken: 't', user: user(42), identity: 'tg:42' }, tgUser(42))).toBe(true);
  });

  it('drops tokens issued for another Telegram account (old bug)', () => {
    expect(isStoredSessionValid({ accessToken: 't', user: user(42), identity: 'tg:42' }, tgUser(7))).toBe(false);
  });

  it('drops tokens whose user payload does not match even if identity was tampered', () => {
    expect(isStoredSessionValid({ accessToken: 't', user: user(1), identity: 'tg:7' }, tgUser(7))).toBe(false);
  });

  it('does not reuse Telegram tokens in a plain browser (guest) and vice versa', () => {
    expect(isStoredSessionValid({ accessToken: 't', user: user(42), identity: 'tg:42' }, { user: null })).toBe(false);
    expect(isStoredSessionValid({ accessToken: 't', user: user(0), identity: 'guest' }, tgUser(42))).toBe(false);
    expect(isStoredSessionValid({ accessToken: 't', user: user(0), identity: 'guest' }, { user: null })).toBe(true);
  });

  it('requires a token', () => {
    expect(isStoredSessionValid({ accessToken: null, user: user(42), identity: 'tg:42' }, tgUser(42))).toBe(false);
    expect(identityOf({ user: null })).toBe('guest');
  });
});
