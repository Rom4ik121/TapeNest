import { QueryClientProvider } from '@tanstack/react-query';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from '@/app/App';
import { initI18n } from '@/i18n';
import { config } from '@/shared/config';
import { queryClient } from '@/shared/api/queryClient';
import { initTelegram } from '@/shared/telegram';
import { startThemeSync } from '@/shared/theme/theme';
import '@/shared/theme/theme.css';

/** Dev mocks only with the flag, via dynamic import (kept out of the main chunk). */
async function enableMocking(): Promise<void> {
  // Static guard first so production builds without VITE_USE_MOCKS drop MSW entirely.
  if (!import.meta.env.DEV && !import.meta.env.VITE_USE_MOCKS) return;
  if (!config.useMocks) return;
  try {
    const { startMocks } = await import('@/mocks/start');
    const mode = await startMocks();
    console.info(`[mocks] API mocked via ${mode}`);
  } catch (err) {
    console.warn('[mocks] failed to start', err);
  }
}

const tg = initTelegram();
startThemeSync();
initI18n(tg.user?.languageCode ?? (typeof navigator !== 'undefined' ? navigator.language : null));

void enableMocking().then(() => {
  const root = document.getElementById('root');
  if (!root) throw new Error('#root missing');
  createRoot(root).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <App />
      </QueryClientProvider>
    </StrictMode>,
  );
});
