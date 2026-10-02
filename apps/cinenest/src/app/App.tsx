import { Clapperboard, RotateCw, Send } from 'lucide-react';
import { lazy, Suspense, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { HashRouter, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import { useAuthStore, type AuthErrorKind } from '@/entities/auth/authStore';
import { ensureSession } from '@/entities/auth/session';
import { CatalogPage } from '@/features/catalog/CatalogPage';
import { TitlePage } from '@/features/title/TitlePage';
import { WatchlistPage } from '@/features/watchlist/WatchlistPage';
import { config } from '@/shared/config';
import { haptic, initTelegram, showBackButton } from '@/shared/telegram';
import { Nav } from './Nav';

/** hls.js (~half of the bundle) is only needed in the player → separate chunk. */
const PlayerPage = lazy(() => import('@/features/player/PlayerPage').then((m) => ({ default: m.PlayerPage })));

const ROOT_TABS = new Set(['/', '/watchlist']);

function Splash({ text }: { text: string }) {
  return (
    <div className="flex min-h-app flex-col items-center justify-center gap-4" role="status" aria-live="polite">
      <div className="grid h-20 w-20 place-items-center rounded-3xl bg-grad-04 text-cream shadow-glow">
        <Clapperboard className="h-10 w-10 animate-pulse" aria-hidden />
      </div>
      <p className="text-sm text-muted">{text}</p>
    </div>
  );
}

const retryAuth = (): void => {
  haptic('light');
  void ensureSession({ tg: initTelegram(), mockAuth: config.isMocked('auth') });
};

function AuthError({ kind }: { kind: AuthErrorKind }) {
  const { t } = useTranslation();
  const bot = config.botUsername;
  return (
    <div role="alert" className="page-enter flex min-h-app flex-col items-center justify-center gap-4 px-8 text-center">
      <div className="grid h-20 w-20 place-items-center rounded-3xl bg-grad-05 text-cream shadow-glow">
        <Clapperboard className="h-10 w-10" aria-hidden />
      </div>
      <h1 className="text-xl font-bold">{t('auth.failedTitle')}</h1>
      <p className="max-w-xs text-muted">{t(`auth.${kind}`)}</p>
      {kind === 'openInTelegram' ? (
        <>
          {bot && (
            <a
              href={`https://t.me/${bot}`}
              className="inline-flex min-h-[44px] items-center gap-2 rounded-full bg-accent px-6 py-3 font-semibold text-accent-fg transition active:scale-95"
            >
              <Send className="h-4 w-4" aria-hidden />
              {t('auth.openBot', { bot })}
            </a>
          )}
          {import.meta.env.DEV && <p className="max-w-xs text-xs text-muted">{t('auth.devHint')}</p>}
        </>
      ) : (
        <button
          type="button"
          onClick={retryAuth}
          className="inline-flex min-h-[44px] items-center gap-2 rounded-full bg-accent px-6 py-3 font-semibold text-accent-fg transition active:scale-95"
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

  // Telegram Back button on nested screens (tabs are roots).
  useEffect(() => {
    if (ROOT_TABS.has(location.pathname)) return;
    return showBackButton(() => navigate(-1));
  }, [location.pathname, navigate]);

  useEffect(() => {
    window.scrollTo(0, 0);
  }, [location.pathname]);

  return (
    <div className="mx-auto flex min-h-app w-full max-w-lg flex-col pt-safe">
      <main className="flex-1 px-4 pb-28 pt-3">
        {/* key → re-mount wrapper so every screen change plays the enter transition */}
        <div key={location.pathname} className="page-enter">
          <Routes location={location}>
            <Route path="/" element={<CatalogPage />} />
            <Route path="/watchlist" element={<WatchlistPage />} />
            <Route path="/title/:titleId" element={<TitlePage />} />
            <Route
              path="/watch/:titleId/:fileId"
              element={
                <Suspense fallback={<div className="fixed inset-0 z-50 bg-ink" />}>
                  <PlayerPage />
                </Suspense>
              }
            />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </div>
      </main>
      <div className="fixed inset-x-0 bottom-0 z-40 mx-auto max-w-lg">
        <Nav />
      </div>
    </div>
  );
}

export function App() {
  const { t } = useTranslation();
  const status = useAuthStore((s) => s.status);
  const error = useAuthStore((s) => s.error);

  useEffect(() => {
    void ensureSession({
      tg: initTelegram(),
      mockAuth: config.isMocked('auth'),
    });
  }, []);

  if (status === 'error') return <AuthError kind={error ?? 'failed'} />;
  if (status !== 'ready') return <Splash text={t('auth.loading')} />;
  return (
    <HashRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
      <Layout />
    </HashRouter>
  );
}
