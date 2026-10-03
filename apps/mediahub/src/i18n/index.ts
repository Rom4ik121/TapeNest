import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import en from './en.json';
import ru from './ru.json';

export type Lang = 'ru' | 'en';

export function pickLanguage(code: string | null | undefined): Lang {
  const c = (code ?? '').toLowerCase();
  if (c.startsWith('en')) return 'en';
  return 'ru';
}

export function initI18n(code: string | null | undefined): typeof i18n {
  const lng = pickLanguage(code);
  if (!i18n.isInitialized) {
    void i18n.use(initReactI18next).init({
      resources: { ru: { translation: ru }, en: { translation: en } },
      lng,
      fallbackLng: 'ru',
      interpolation: { escapeValue: false },
      returnNull: false,
    });
  } else if (i18n.language !== lng) {
    void i18n.changeLanguage(lng);
  }
  if (typeof document !== 'undefined') document.documentElement.lang = lng;
  return i18n;
}

export { i18n };
