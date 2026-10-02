import { cn } from '@/shared/lib/cn';

export function EqBars({ active, className }: { active: boolean; className?: string }) {
  return (
    <span className={cn('inline-flex h-3.5 items-end gap-[2px]', className)} aria-hidden>
      {[0, 0.2, 0.4].map((d) => (
        <span
          key={d}
          className={cn('w-[3px] origin-bottom rounded-full bg-current', active ? 'animate-eq' : 'scale-y-50')}
          style={{ height: '100%', animationDelay: `${d}s` }}
        />
      ))}
    </span>
  );
}
