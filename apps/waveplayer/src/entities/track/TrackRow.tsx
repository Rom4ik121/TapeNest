import { Heart, MoreHorizontal } from 'lucide-react';
import { memo } from 'react';
import { useTranslation } from 'react-i18next';
import type { Track } from '@/shared/api/types';
import { cn } from '@/shared/lib/cn';
import { formatTime } from '@/shared/lib/format';
import { Cover } from '@/shared/ui/Cover';
import { haptic } from '@/shared/telegram';
import { EqBars } from '@/shared/ui/EqBars';

interface Props {
  track: Track;
  active: boolean;
  playing: boolean;
  onPlay: (track: Track) => void;
  onLike: (track: Track) => void;
  onMore: (track: Track) => void;
}

export const TrackRow = memo(function TrackRow({ track, active, playing, onPlay, onLike, onMore }: Props) {
  const { t } = useTranslation();
  return (
    <li
      className={cn(
        'group flex items-center gap-3 rounded-2xl px-2 py-1.5 transition-colors',
        active && 'bg-fg/[0.06]',
      )}
    >
      <button
        type="button"
        onClick={() => {
          haptic('light');
          onPlay(track);
        }}
        className="flex min-w-0 flex-1 items-center gap-3 text-left active:opacity-70"
      >
        <div className="relative">
          <Cover id={track.id} src={track.coverUrl} alt="" className="h-12 w-12" />
          {active && (
            <div className="absolute inset-0 flex items-center justify-center rounded-xl bg-ink/45 text-cream">
              <EqBars active={playing} />
            </div>
          )}
        </div>
        <div className="min-w-0 flex-1">
          <p className={cn('truncate text-[15px] font-semibold leading-tight', active && 'text-accent-ink')}>
            {track.title}
          </p>
          <p className="mt-0.5 truncate text-[13px] text-muted">
            {track.artist}
            <span className="mx-1.5 opacity-60">·</span>
            {formatTime(track.durationSec)}
          </p>
        </div>
      </button>
      <button
        type="button"
        onClick={() => {
          haptic(track.liked ? 'light' : 'success');
          onLike(track);
        }}
        aria-label={track.liked ? t('track.unlike') : t('track.like')}
        aria-pressed={track.liked}
        className="grid h-10 w-10 place-items-center rounded-full text-muted active:scale-90"
      >
        <Heart className={cn('h-5 w-5', track.liked && 'fill-magenta text-magenta')} />
      </button>
      <button
        type="button"
        onClick={() => {
          haptic('selection');
          onMore(track);
        }}
        aria-label={t('track.more')}
        className="-ml-2 grid h-10 w-8 place-items-center rounded-full text-muted active:scale-90"
      >
        <MoreHorizontal className="h-5 w-5" aria-hidden />
      </button>
    </li>
  );
});
