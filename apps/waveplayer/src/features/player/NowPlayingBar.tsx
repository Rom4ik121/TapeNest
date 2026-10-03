import { SkipForward } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { playerStore, useCurrentTrack, usePlayer } from '@/entities/player';
import { cn } from '@/shared/lib/cn';
import { Cover } from '@/shared/ui/Cover';
import { haptic } from '@/shared/telegram';
import { PlayButton } from './PlayButton';

/** Persistent mini player above the tab bar. Survives navigation. */
export function NowPlayingBar() {
  const { t } = useTranslation();
  const track = useCurrentTrack();
  const status = usePlayer((s) => s.status);
  const pos = usePlayer((s) => s.positionSec);
  const dur = usePlayer((s) => s.durationSec);
  const wave = usePlayer((s) => s.waveSessionId !== null);
  if (!track) return null;

  const progress = dur > 0 ? Math.min(100, (pos / dur) * 100) : 0;
  const { toggle, next, openFullPlayer } = playerStore.getState();

  return (
    <div className="mx-2 mb-2 overflow-hidden rounded-2xl bg-glass shadow-card ring-1 ring-fg/5 animate-fade-in">
      <div className="flex items-center gap-3 p-2 pr-3">
        <button
          type="button"
          onClick={() => {
            haptic('light');
            openFullPlayer(true);
          }}
          aria-label={t('player.open')}
          className="flex min-w-0 flex-1 items-center gap-3 text-left"
        >
          <Cover
            id={track.id}
            src={track.coverUrl}
            alt=""
            className={cn('h-11 w-11', status === 'playing' && 'shadow-glow')}
          />
          <div className="min-w-0">
            <p className="truncate text-[15px] font-semibold leading-tight">{track.title}</p>
            <p className="truncate text-[13px] text-muted">
              {status === 'blocked'
                ? t('player.tapToPlay')
                : status === 'error'
                  ? t('player.streamError')
                  : wave
                    ? `${t('player.fromWave')} · ${track.artist}`
                    : track.artist}
            </p>
          </div>
        </button>
        <PlayButton
          status={status}
          onClick={toggle}
          className="h-11 w-11 bg-accent text-accent-fg"
          iconClassName="h-5 w-5"
        />
        <button
          type="button"
          onClick={() => {
            haptic('light');
            void next();
          }}
          aria-label={t('player.next')}
          className="grid h-10 w-10 place-items-center rounded-full active:scale-90"
        >
          <SkipForward className="h-5 w-5 fill-current" aria-hidden />
        </button>
      </div>
      <div className="h-[3px] bg-fg/10">
        {/* New node per track so the fill snaps to 0 instead of easing down from the previous track. */}
        <div
          key={track.id}
          className="h-full bg-grad-02 transition-[width] duration-300 ease-linear"
          style={{ width: `${progress}%` }}
        />
      </div>
    </div>
  );
}
