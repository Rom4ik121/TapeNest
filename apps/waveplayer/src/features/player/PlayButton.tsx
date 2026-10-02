import { Loader2, Pause, Play } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { PlayerStatus } from '@/entities/player';
import { cn } from '@/shared/lib/cn';
import { haptic } from '@/shared/telegram';

interface Props {
  status: PlayerStatus;
  onClick: () => void;
  className?: string;
  iconClassName?: string;
}

export function PlayButton({ status, onClick, className, iconClassName = 'h-6 w-6' }: Props) {
  const { t } = useTranslation();
  const playing = status === 'playing';
  const loading = status === 'loading';
  return (
    <button
      type="button"
      onClick={() => {
        haptic('medium');
        onClick();
      }}
      aria-busy={loading}
      aria-label={playing ? t('player.pause') : t('player.play')}
      className={cn('grid place-items-center rounded-full transition-transform active:scale-90', className)}
    >
      {loading ? (
        <Loader2 className={cn('animate-spin', iconClassName)} aria-hidden />
      ) : playing ? (
        <Pause className={cn('fill-current', iconClassName)} aria-hidden />
      ) : (
        <Play className={cn('translate-x-[1px] fill-current', iconClassName)} aria-hidden />
      )}
    </button>
  );
}
