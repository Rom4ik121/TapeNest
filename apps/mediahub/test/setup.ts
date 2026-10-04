import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/react';
import { afterEach } from 'vitest';
import { initI18n } from '@/i18n';

initI18n('ru');

afterEach(() => {
  if (typeof document === 'undefined') return; // node-environment test files
  cleanup();
  localStorage.clear();
});
