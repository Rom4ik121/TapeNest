import { useTranslation } from 'react-i18next';
import { cn } from '@/shared/lib/cn';

export function Skeleton({ className }: { className?: string }) {
  return <div aria-hidden className={cn('animate-pulse rounded-lg bg-fg/10', className)} />;
}

export function TrackListSkeleton({ rows = 6 }: { rows?: number }) {
  const { t } = useTranslation();
  return (
    <div className="space-y-1" role="status" aria-busy="true" aria-label={t('a11y.skeleton')}>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex items-center gap-3 px-2 py-2">
          <Skeleton className="h-12 w-12 rounded-xl" />
          <div className="flex-1 space-y-2">
            <Skeleton className="h-3.5 w-2/3" />
            <Skeleton className="h-3 w-1/3" />
          </div>
        </div>
      ))}
    </div>
  );
}
