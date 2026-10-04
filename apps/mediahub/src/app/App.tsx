import { Clapperboard, RotateCw, Send } from 'lucide-react';
import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { HashRouter, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import { useAuthStore, type AuthErrorKind } from '@/entities/auth/authStore';
import { ensureSession } from '@/entities/auth/session';
import { LibraryPage } from '@/features/library/LibraryPage';
import { PhotoEditorPage } from '@/features/photo/PhotoEditorPage';
import { VideoEditorPage } from '@/features/video/VideoEditorPage';
import { config } from '@/shared/config';
import { haptic, initTelegram, showBackButton } from '@/shared/telegram';

function Splash({ text }: { text: string }) {
  return (
    <div className="flex min-h-app flex-col items-center justify-center gap-4" role="status">
      <div className="grid h-20 w-20 place-items-center rounded-3xl bg-grad-04 text-cream shadow-glow">
        <Clapperboard className="h-10 w-10 animate-pulse" aria-hidden />
      </div>
      <p className="text-sm text-muted">{text}</p>
    </div>
  );
}

function AuthError({ kind }: { kind: AuthErrorKind }) {
  const { t } = useTranslation();
  const bot = config.botUsername;
  return (
    <div role="alert" className="flex min-h-app flex-col items-center justify-center gap-4 px-8 text-center">
      <h1 className="text-xl font-bold">{t('auth.failedTitle')}</h1>
      <p className="max-w-xs text-muted">{t(`auth.${kind}`)}</p>
      {kind === 'openInTelegram' && bot ? (
        <a href={`https://t.me/${bot}`} className="inline-flex min-h-11 items-center gap-2 rounded-full bg-accent px-6 font-semibold text-accent-fg">
          <Send className="h-4 w-4" aria-hidden />
          {t('auth.openBot', { bot })}
        </a>
      ) : (
        <button
          type="button"
          onClick={() => {
            haptic('light');
            void ensureSession({ tg: initTelegram(), mockAuth: false });
          }}
          className="inline-flex min-h-11 items-center gap-2 rounded-full bg-accent px-6 font-semibold text-accent-fg"
        >
          <RotateCw className="h-4 w-4" aria-hidden />
          {t('auth.retry')}
        </button>
      )}
    </div>
  );
}

function Layout() {
  const location = useLocation();
  const navigate = useNavigate();
  useEffect(() => {
    if (location.pathname === '/') return;
    return showBackButton(() => navigate(-1));
  }, [location.pathname, navigate]);
  return (
    <div className="mx-auto flex min-h-app w-full max-w-lg flex-col px-4 pb-8 pt-safe">
      <Routes>
        <Route path="/" element={<LibraryPage />} />
        <Route path="/v/:jobId" element={<VideoEditorPage />} />
        <Route path="/p/:photoId" element={<PhotoEditorPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </div>
  );
}

export function App() {
  const { t } = useTranslation();
  const status = useAuthStore((s) => s.status);
  const error = useAuthStore((s) => s.error);
  useEffect(() => {
    void ensureSession({ tg: initTelegram(), mockAuth: config.isMocked('auth') });
  }, []);
  if (status === 'error') return <AuthError kind={error ?? 'failed'} />;
  if (status !== 'ready') return <Splash text={t('auth.loading')} />;
  return (
    <HashRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <Layout />
    </HashRouter>
  );
}
