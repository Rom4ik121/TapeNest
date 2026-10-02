import { ChevronDown, Heart, ListPlus, SkipBack, SkipForward, Waves } from 'lucide-react';
import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { playerStore, useCurrentTrack, usePlayer } from '@/entities/player';
import { AddToPlaylistSheet } from '@/entities/track/AddToPlaylistSheet';
import { cn } from '@/shared/lib/cn';
import { formatTime, gradientFor } from '@/shared/lib/format';
import { haptic, showBackButton } from '@/shared/telegram';
import { Cover } from '@/shared/ui/Cover';
import { PlayButton } from './PlayButton';

/** Full-screen player over a brand-gradient backdrop with the blurred cover. */
export function FullPlayer() {
  const { t } = useTranslation();
  const open = usePlayer((s) => s.fullPlayerOpen);
  const track = useCurrentTrack();
  const status = usePlayer((s) => s.status);
  const pos = usePlayer((s) => s.positionSec);
  const dur = usePlayer((s) => s.durationSec);
  const buffering = usePlayer((s) => s.buffering);
  const wave = usePlayer((s) => s.waveSessionId !== null);
  const [dragValue, setDragValue] = useState<number | null>(null);
  const [menu, setMenu] = useState(false);
  const { toggle, next, prev, seek, toggleLikeCurrent, openFullPlayer } = playerStore.getState();

  useEffect(() => {
    if (!open) return;
    const close = (): void => openFullPlayer(false);
    const onKey = (e: KeyboardEvent): void => {
      if (e.key === 'Escape') close();
    };
    window.addEventListener('keydown', onKey);
    const off = showBackButton(close);
    return () => {
      window.removeEventListener('keydown', onKey);
      off();
    };
  }, [open, openFullPlayer]);

  if (!open || !track) return null;
  const shown = dragValue ?? pos;
  const duration = dur || track.durationSec;
  const progress = duration > 0 ? (shown / duration) * 100 : 0;

  return createPortal(
    <div
      className="on-gradient fixed inset-0 z-50 flex animate-slide-up flex-col overflow-hidden text-cream"
      role="dialog"
      aria-modal="true"
      aria-label={t('player.nowPlaying')}
    >
      <div className={cn('absolute inset-0', gradientFor(track.id))} />
      {track.coverUrl && (
        <img
          src={track.coverUrl}
          alt=""
          aria-hidden
          className="absolute inset-0 h-full w-full scale-125 object-cover opacity-70 blur-3xl saturate-150"
        />
      )}
      <div className="absolute inset-0 bg-gradient-to-b from-ink/30 via-ink/10 to-ink/85" />

      <div className="relative mx-auto flex w-full max-w-lg flex-1 flex-col px-6 pb-safe pt-[calc(var(--app-safe-top)+1rem)]">
        <header className="flex items-center justify-between">
          <button
            type="button"
            onClick={() => openFullPlayer(false)}
            aria-label={t('player.close')}
            className="grid h-10 w-10 place-items-center rounded-full bg-cream/10 active:scale-90"
          >
            <ChevronDown className="h-6 w-6" />
          </button>
          <p className="flex items-center gap-1.5 text-xs font-bold uppercase tracking-[0.18em] text-cream/80">
            {wave && <Waves className="h-4 w-4" />}
            {wave ? t('player.fromWave') : t('player.nowPlaying')}
          </p>
          <button
            type="button"
            onClick={() => setMenu(true)}
            aria-label={t('track.addToPlaylist')}
            className="grid h-10 w-10 place-items-center rounded-full bg-cream/10 active:scale-90"
          >
            <ListPlus className="h-5 w-5" />
          </button>
        </header>

        <div className="flex flex-1 items-center justify-center py-6">
          <Cover
            id={track.id}
            src={track.coverUrl}
            alt={track.title}
            rounded="rounded-[2rem]"
            className={cn(
              'aspect-square w-full max-w-[20rem] shadow-[0_30px_80px_-20px_rgba(0,0,0,0.6)] transition-transform duration-500',
              status === 'playing' ? 'scale-100' : 'scale-[0.92]',
            )}
          />
        </div>

        <div className="flex items-end justify-between gap-4">
          <div className="min-w-0">
            <h2 className="truncate text-2xl font-extrabold tracking-tight">{track.title}</h2>
            <p className="mt-1 truncate text-base text-cream/75">
              {track.artist}
              {track.album ? ` — ${track.album}` : ''}
            </p>
          </div>
          <button
            type="button"
            onClick={() => {
              haptic(track.liked ? 'light' : 'success');
              toggleLikeCurrent();
            }}
            aria-pressed={track.liked}
            aria-label={track.liked ? t('track.unlike') : t('track.like')}
            className="grid h-12 w-12 shrink-0 place-items-center rounded-full bg-cream/10 active:scale-90"
          >
            <Heart className={cn('h-6 w-6', track.liked && 'fill-magenta text-magenta')} />
          </button>
        </div>

        <div className="mt-5 text-amber">
          <input
            type="range"
            className="seek w-full"
            min={0}
            max={Math.max(1, duration)}
            step={0.5}
            value={Math.min(shown, duration)}
            aria-label={t('player.seek')}
            style={{ ['--progress' as string]: `${progress}%` }}
            onChange={(e) => setDragValue(Number(e.target.value))}
            onPointerUp={() => {
              if (dragValue !== null) seek(dragValue);
              setDragValue(null);
            }}
            onKeyUp={() => {
              if (dragValue !== null) seek(dragValue);
              setDragValue(null);
            }}
            onTouchEnd={() => {
              if (dragValue !== null) seek(dragValue);
              setDragValue(null);
            }}
          />
          <div className="mt-1 flex justify-between text-xs tabular-nums text-cream/70">
            <span>{formatTime(shown)}</span>
            <span>
              {status === 'error'
                ? t('player.streamError')
                : status === 'blocked'
                  ? t('player.tapToPlay')
                  : buffering && status === 'playing'
                    ? t('player.loading')
                    : ''}
            </span>
            <span>-{formatTime(Math.max(0, duration - shown))}</span>
          </div>
        </div>

        <div className="mb-6 mt-4 flex items-center justify-between px-2">
          <button
            type="button"
            onClick={() => {
              haptic('light');
              prev();
            }}
            aria-label={t('player.prev')}
            className="grid h-14 w-14 place-items-center rounded-full active:scale-90"
          >
            <SkipBack className="h-8 w-8 fill-current" aria-hidden />
          </button>
          <PlayButton
            status={status}
            onClick={toggle}
            className="h-20 w-20 bg-cream text-ink shadow-glow"
            iconClassName="h-9 w-9"
          />
          <button
            type="button"
            onClick={() => {
              haptic('light');
              void next();
            }}
            aria-label={t('player.next')}
            className="grid h-14 w-14 place-items-center rounded-full active:scale-90"
          >
            <SkipForward className="h-8 w-8 fill-current" aria-hidden />
          </button>
        </div>
      </div>
      <AddToPlaylistSheet track={menu ? track : null} onClose={() => setMenu(false)} />
    </div>,
    document.body,
  );
}
