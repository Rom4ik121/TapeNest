import { Bookmark } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useWatchlist } from '@/entities/title/queries';
import { TitleGrid, TitleGridSkeleton } from '@/entities/title/TitleGrid';
import { LoadMore } from '@/shared/ui/LoadMore';
import { EmptyState, ErrorState } from '@/shared/ui/States';

export function WatchlistPage() {
  const { t } = useTranslation();
  const list = useWatchlist();
  return (
    <div>
      <header className="px-1 pt-2">
        <h1 className="text-[28px] font-extrabold tracking-tight">{t('watchlist.title')}</h1>
        {list.isSuccess && <p className="text-sm text-muted">{t('watchlist.count', { count: list.titles.length })}</p>}
      </header>
      <div className="mt-5">
        {list.isPending && <TitleGridSkeleton count={6} />}
        {list.isError && (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} retrying={list.isFetching} />
        )}
        {list.isSuccess && list.titles.length === 0 && (
          <EmptyState icon={<Bookmark className="h-10 w-10 opacity-40" aria-hidden />}>
            {t('watchlist.empty')}
          </EmptyState>
        )}
        {list.isSuccess && list.titles.length > 0 && (
          <>
            <TitleGrid titles={list.titles} />
            <LoadMore
              hasMore={list.hasNextPage}
              loading={list.isFetchingNextPage}
              onLoad={() => void list.fetchNextPage()}
            />
          </>
        )}
      </div>
    </div>
  );
}
