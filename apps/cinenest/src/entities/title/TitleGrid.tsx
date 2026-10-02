import { Bookmark, Star } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import type { TitleSummary } from '@/shared/api/types';
import { formatRating } from '@/shared/lib/format';
import { haptic } from '@/shared/telegram';
import { Poster } from '@/shared/ui/Poster';
import { Skeleton } from '@/shared/ui/Skeleton';

export function TitleCard({ title }: { title: TitleSummary }) {
  const { t, i18n } = useTranslation();
  const rating = formatRating(title.rating, i18n.language);
  return (
    <Link
      to={`/title/${encodeURIComponent(title.id)}`}
      onClick={() => haptic('selection')}
      className="group block rounded-2xl transition active:scale-[0.97]"
      aria-label={`${title.title}${title.year ? `, ${title.year}` : ''}`}
    >
      <div className="relative">
        <Poster id={title.id} src={title.posterUrl} alt="" className="rounded-2xl shadow-card" />
        {rating && (
          <span className="absolute left-2 top-2 inline-flex items-center gap-1 rounded-full bg-ink/65 px-2 py-0.5 text-[11px] font-bold text-cream backdrop-blur">
            <Star className="h-3 w-3 fill-amber text-amber" aria-hidden /> {rating}
          </span>
        )}
        {title.inWatchlist && (
          <span className="absolute right-2 top-2 grid h-6 w-6 place-items-center rounded-full bg-accent text-accent-fg">
            <Bookmark className="h-3.5 w-3.5 fill-current" aria-hidden />
          </span>
        )}
      </div>
      <p className="mt-2 line-clamp-2 text-sm font-semibold leading-tight">{title.title}</p>
      <p className="mt-0.5 text-xs text-muted">{[title.year, t(`title.${title.kind}`)].filter(Boolean).join(' · ')}</p>
    </Link>
  );
}

export function TitleGrid({ titles }: { titles: TitleSummary[] }) {
  return (
    <ul className="grid grid-cols-3 gap-x-3 gap-y-5">
      {titles.map((tt) => (
        <li key={tt.id}>
          <TitleCard title={tt} />
        </li>
      ))}
    </ul>
  );
}

export function TitleGridSkeleton({ count = 9 }: { count?: number }) {
  const { t } = useTranslation();
  return (
    <div className="grid grid-cols-3 gap-x-3 gap-y-5" role="status" aria-busy="true" aria-label={t('a11y.skeleton')}>
      {Array.from({ length: count }, (_, i) => (
        <div key={i}>
          <Skeleton className="aspect-[2/3] w-full rounded-2xl" />
          <Skeleton className="mt-2 h-3.5 w-4/5" />
          <Skeleton className="mt-1.5 h-3 w-1/2" />
        </div>
      ))}
    </div>
  );
}
