import { AlertTriangle, RotateCw } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { WarmupView } from '@/entities/player/warmup';
import type { StreamSession } from '@/shared/api/types';
import { formatSpeed } from '@/shared/lib/format';

const R = 44;
const C = 2 * Math.PI * R;

/** TorrServer warm-up indicator (spec §5.5/§6): progress ring, peers, speed, ETA. */
export function WarmupOverlay({
  view,
  session,
  onRetry,
}: {
  view: WarmupView;
  session: StreamSession | undefined;
  onRetry: () => void;
}) {
  const { t, i18n } = useTranslation();
  if (view.phase === 'ready') return null;
  if (view.phase === 'failed') {
    return (
      <div role="alert" className="absolute inset-0 grid place-items-center bg-ink/80 px-8 text-center text-cream">
        <div className="flex flex-col items-center gap-3">
          <AlertTriangle className="h-10 w-10 text-amber" aria-hidden />
          <p className="text-lg font-bold">{t('player.failed')}</p>
          <p className="max-w-xs text-sm text-cream/75">{t('player.failedHint')}</p>
          <button
            type="button"
            onClick={onRetry}
            className="mt-2 inline-flex min-h-[44px] items-center gap-2 rounded-full bg-amber px-5 font-semibold text-ink transition active:scale-95"
          >
            <RotateCw className="h-4 w-4" aria-hidden /> {t('player.retry')}
          </button>
        </div>
      </div>
    );
  }
  const label =
    view.phase === 'connecting' ? t('player.connecting') : t('player.buffering', { pct: Math.round(view.pct) });
  return (
    <div className="absolute inset-0 grid place-items-center bg-ink/70 px-8 text-center text-cream">
      <div className="flex flex-col items-center gap-3" role="status" aria-live="polite">
        <svg viewBox="0 0 100 100" className="h-28 w-28 -rotate-90" aria-hidden>
          <circle cx="50" cy="50" r={R} fill="none" stroke="rgb(231 228 222 / 0.15)" strokeWidth="6" />
          <circle
            cx="50"
            cy="50"
            r={R}
            fill="none"
            stroke="url(#warm)"
            strokeWidth="6"
            strokeLinecap="round"
            strokeDasharray={C}
            strokeDashoffset={view.phase === 'connecting' ? C * 0.85 : C * (1 - view.pct / 100)}
            className={
              view.phase === 'connecting'
                ? 'origin-center animate-spin [animation-duration:1.4s]'
                : 'transition-[stroke-dashoffset] duration-700'
            }
          />
          <defs>
            <linearGradient id="warm" x1="0" y1="0" x2="1" y2="1">
              <stop offset="0" stopColor="#EEAA11" />
              <stop offset="1" stopColor="#BB3381" />
            </linearGradient>
          </defs>
        </svg>
        <p className="text-base font-bold">{label}</p>
        {session && view.phase === 'buffering' && (
          <p className="text-xs tabular-nums text-cream/75">
            {[
              t('player.peers', { n: session.peers }),
              t('player.speed', { speed: formatSpeed(session.speedBps, i18n.language) }),
              view.etaSec !== null ? t('player.eta', { sec: view.etaSec }) : null,
            ]
              .filter(Boolean)
              .join(' · ')}
          </p>
        )}
        <p className="max-w-xs text-xs text-cream/60">{t('player.hint')}</p>
      </div>
    </div>
  );
}
