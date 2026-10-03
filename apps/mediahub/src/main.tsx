import { QueryClientProvider } from '@tanstack/react-query';
import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from '@/app/App';
import { initI18n } from '@/i18n';
import { queryClient } from '@/shared/api/queryClient';
import { initTelegram } from '@/shared/telegram';
import { startThemeSync } from '@/shared/theme/theme';
import '@/shared/theme/theme.css';

const tg = initTelegram();
startThemeSync();
initI18n(tg.user?.languageCode ?? (typeof navigator !== 'undefined' ? navigator.language : null));

const root = document.getElementById('root');
if (!root) throw new Error('#root missing');
createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
