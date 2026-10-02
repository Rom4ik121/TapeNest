import { describe, expect, it } from 'vitest';
import en from './en.json';
import ru from './ru.json';
import { i18n, pickLanguage } from './index';

const keys = (o: object, prefix = ''): string[] =>
  Object.entries(o).flatMap(([k, v]) =>
    v && typeof v === 'object' ? keys(v as object, `${prefix}${k}.`) : [`${prefix}${k}`],
  );
const base = (k: string): string => k.replace(/_(one|few|many|other)$/, '');

describe('i18n', () => {
  it('ru and en have the same keys (plural forms aside)', () => {
    expect([...new Set(keys(ru).map(base))].sort()).toEqual([...new Set(keys(en).map(base))].sort());
  });

  it('language from Telegram language_code, fallback ru', () => {
    expect(pickLanguage('en')).toBe('en');
    expect(pickLanguage('en-US')).toBe('en');
    expect(pickLanguage('ru')).toBe('ru');
    expect(pickLanguage('uz')).toBe('ru');
    expect(pickLanguage(null)).toBe('ru');
  });

  it('Russian plurals', () => {
    const t = i18n.getFixedT('ru');
    expect(t('watchlist.count', { count: 1 })).toBe('1 фильм');
    expect(t('watchlist.count', { count: 3 })).toBe('3 фильма');
    expect(t('watchlist.count', { count: 5 })).toBe('5 фильмов');
    expect(i18n.getFixedT('en')('watchlist.count', { count: 2 })).toBe('2 titles');
  });
});
