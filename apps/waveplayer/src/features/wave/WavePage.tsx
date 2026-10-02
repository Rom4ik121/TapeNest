import { Heart, SkipForward, Sparkles, Waves } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { playerStore, useCurrentTrack, usePlayer } from '@/entities/player';
import { PlayButton } from '@/features/player/PlayButton';
import { WAVE_MODES, type WaveMode } from '@/shared/api/types';
import { cn } from '@/shared/lib/cn';
import { haptic } from '@/shared/telegram';
import { Cover } from '@/shared/ui/Cover';
import { reasonText } from './reason';

/** "My Wave" — full-bleed gradient 04 with animated rings. */
export function WavePage() {
  const { t } = useTranslation();
  const active = usePlayer((s) => s.waveSessionId !== null || s.waveFallback);
  const status = usePlayer((s) => s.status);
  const fallback = usePlayer((s) => s.waveFallback);
  const track = useCurrentTrack();
  const queue = usePlayer((s) => s.queue);
  const index = usePlayer((s) => s.index);
  const inWave = usePlayer((s) => s.waveSessionId !== null);
  const upNext = inWave ? queue.slice(index + 1, index + 4) : [];
  const playing = active && status === 'playing';
  const mode = usePlayer((s) => s.waveMode);
  const reason = active && track ? reasonText(t, track) : null;

  const onMode = (m: WaveMode): void => {
    if (m === mode && active) return;
    haptic('light');
    void playerStore.getState().startWave(m);
  };

  const onMain = (): void => {
    const st = playerStore.getState();
    if (active) st.toggle();
    else void st.startWave();
  };

  return (
    <div className="scrim on-gradient relative -mx-4 -mt-3 min-h-[calc(var(--app-height)-5rem)] overflow-hidden bg-grad-04 px-6 pb-40 pt-8 text-cream">
      {/* ambient blobs */}
      <div className="pointer-events-none absolute -left-24 top-10 h-72 w-72 animate-float rounded-full bg-amber/30 blur-3xl" />
      <div className="pointer-events-none absolute -right-20 top-60 h-72 w-72 animate-float rounded-full bg-magenta/40 blur-3xl [animation-delay:-2s]" />
      <div className="pointer-events-none absolute bottom-0 left-10 h-60 w-60 animate-float rounded-full bg-teal/20 blur-3xl [animation-delay:-4s]" />

      <div className="relative mx-auto max-w-lg">
        <p className="flex items-center gap-2 text-xs font-bold uppercase tracking-[0.2em] text-cream/85">
          <Waves className="h-4 w-4" aria-hidden /> TapeNest
        </p>
        <h1 className="mt-2 text-4xl font-extrabold tracking-tight">{t('wave.title')}</h1>
        <p className="mt-1 text-cream/85">{t('wave.subtitle')}</p>

        <div
          role="radiogroup"
          aria-label={t('wave.modes.label')}
          className="-mx-6 mt-5 flex gap-2 overflow-x-auto px-6 pb-1 [scrollbar-width:none]"
        >
          {WAVE_MODES.map((m) => (
            <button
              key={m}
              type="button"
              role="radio"
              aria-checked={m === mode}
              onClick={() => onMode(m)}
              className={cn(
                'shrink-0 rounded-full px-4 py-1.5 text-sm font-semibold backdrop-blur transition active:scale-95',
                m === mode ? 'bg-cream text-ink shadow-glow' : 'bg-cream/15 text-cream hover:bg-cream/25',
              )}
            >
              {t(`wave.modes.${m}`)}
            </button>
          ))}
        </div>

        <div className="relative mx-auto mt-10 grid h-64 w-64 place-items-center">
          {/* static rings + a soft ripple while playing (no scale-2x ping) */}
          {[0, 1, 2].map((i) => (
            <span
              key={i}
              aria-hidden
              className="absolute rounded-full border border-cream/20"
              style={{ inset: `${i * 20}px` }}
            />
          ))}
          {playing &&
            [0, 1].map((i) => (
              <span
                key={`r${i}`}
                aria-hidden
                className="absolute inset-6 animate-ripple rounded-full border-2 border-cream/40"
                style={{ animationDelay: `${i * 1.4}s` }}
              />
            ))}
          {/* vinyl disc: grooves + the current cover as the label */}
          <div
            aria-hidden
            className={cn(
              'absolute inset-8 rounded-full bg-ink/40 shadow-2xl ring-1 ring-cream/15',
              '[background-image:repeating-radial-gradient(circle,rgb(231_228_222/0.07)_0_1px,transparent_1px_5px)]',
              playing && 'animate-spin-slow',
            )}
          >
            {active && track && (
              // Cover's own `relative` would override `absolute` → position a wrapper instead.
              <div className="absolute inset-[12%]">
                <Cover
                  id={track.id}
                  src={track.coverUrl}
                  alt=""
                  rounded="rounded-full"
                  className="h-full w-full ring-2 ring-cream/30"
                />
              </div>
            )}
          </div>
          <PlayButton
            status={active ? status : 'paused'}
            onClick={onMain}
            className="relative h-24 w-24 bg-cream text-ink shadow-glow"
            iconClassName="h-10 w-10"
          />
        </div>

        {!active && <p className="mt-8 text-center text-sm text-cream/85">{t('wave.hint')}</p>}

        {active && track && (
          <div className="mt-8 text-center" aria-live="polite">
            <h2 className="truncate text-2xl font-bold">{track.title}</h2>
            <p className="truncate text-cream/80">{track.artist}</p>
            {reason && (
              <p className="mx-auto mt-3 inline-flex max-w-full items-center gap-1.5 rounded-full bg-ink/30 px-3 py-1 text-xs font-medium text-cream/90 backdrop-blur">
                <Sparkles className="h-3.5 w-3.5 shrink-0" aria-hidden />
                <span className="truncate">{reason}</span>
              </p>
            )}
            <div className="mt-5 flex justify-center gap-4">
              <button
                type="button"
                onClick={() => {
                  haptic(track.liked ? 'light' : 'success');
                  playerStore.getState().toggleLikeCurrent();
                }}
                aria-pressed={track.liked}
                aria-label={track.liked ? t('track.unlike') : t('track.like')}
                className="grid h-14 w-14 place-items-center rounded-full bg-cream/15 backdrop-blur active:scale-90"
              >
                <Heart className={cn('h-6 w-6', track.liked && 'fill-cream')} aria-hidden />
              </button>
              <button
                type="button"
                onClick={() => {
                  haptic('light');
                  void playerStore.getState().next();
                }}
                aria-label={t('player.next')}
                className="grid h-14 w-14 place-items-center rounded-full bg-cream/15 backdrop-blur active:scale-90"
              >
                <SkipForward className="h-6 w-6 fill-current" aria-hidden />
              </button>
            </div>
            {fallback && <p className="mt-4 rounded-2xl bg-ink/30 px-4 py-2 text-sm">{t('wave.fallback')}</p>}
          </div>
        )}

        {upNext.length > 0 && (
          <div className="mt-10 rounded-3xl bg-ink/35 p-4 backdrop-blur">
            <p className="mb-2 text-xs font-bold uppercase tracking-[0.18em] text-cream/75">{t('wave.upNext')}</p>
            <ul className="space-y-2">
              {upNext.map((tr) => (
                <li key={tr.id} className="flex items-center gap-3">
                  <Cover id={tr.id} src={tr.coverUrl} alt="" className="h-10 w-10" />
                  <div className="min-w-0">
                    <p className="truncate text-sm font-semibold">{tr.title}</p>
                    <p className="truncate text-xs text-cream/70">
                      {tr.artist}
                      {reasonText(t, tr) && <span className="text-cream/55"> · {reasonText(t, tr)}</span>}
                    </p>
                  </div>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </div>
  );
}
