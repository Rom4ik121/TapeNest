import { describe, expect, it } from 'vitest';
import en from './en.json';
import ru from './ru.json';
import { pickLanguage } from './index';

const keys = (o: object): string[] => Object.keys(o).sort();

describe('i18n', () => {
  it('ru and en have the same keys', () => {
    expect(keys(ru)).toEqual(keys(en));
  });

  it('falls back to Russian', () => {
    expect(pickLanguage('en-US')).toBe('en');
    expect(pickLanguage('uz')).toBe('ru');
    expect(pickLanguage(null)).toBe('ru');
  });
});
