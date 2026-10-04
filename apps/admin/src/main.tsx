import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from '@/app/App';
import { initTelegram } from '@/shared/telegram';
import '@/index.css';

initTelegram();

const root = document.getElementById('root');
if (!root) throw new Error('root missing');

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
