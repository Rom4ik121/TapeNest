import { Film, Search, X } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useCatalog } from '@/entities/title/queries';
import { TitleGrid, TitleGridSkeleton } from '@/entities/title/TitleGrid';
import type { TitleKind } from '@/shared/api/types';
import { config } from '@/shared/config';
import { cn } from '@/shared/lib/cn';
import { useDebouncedValue } from '@/shared/lib/useDebouncedValue';
import { haptic } from '@/shared/telegram';
import { LoadMore } from '@/shared/ui/LoadMore';
import { SectionHeader } from '@/shared/ui/SectionHeader';
import { EmptyState, ErrorState } from '@/shared/ui/States';
import { ContinueRail } from './ContinueRail';

const KINDS: Array<TitleKind | null> = [null, 'movie', 'series'];

export function CatalogPage() {
  const { t } = useTranslation();
  const [q, setQ] = useState('');
  const [kind, setKind] = useState<TitleKind | null>(null);
  const term = useDebouncedValue(q, 300).trim();
  const catalog = useCatalog({ q: term, kind });
  const browsing = term === '' && kind === null;

  return (
    <div>
      <header className="px-1 pt-2">
        <p className="text-sm font-medium text-muted">{t('catalog.subtitle')}</p>
        <h1 className="mt-0.5 text-[28px] font-extrabold leading-tight tracking-tight">{t('catalog.title')}</h1>
        {config.isMocked('cinema') && (
          <span className="mt-2 inline-flex items-center gap-1.5 rounded-full bg-teal/15 px-2.5 py-1 text-[11px] font-semibold text-fg">
            <span className="h-1.5 w-1.5 rounded-full bg-teal" aria-hidden /> {t('catalog.demo')}
          </span>
        )}
      </header>

      <label className="sticky top-2 z-10 mt-4 flex items-center gap-2 rounded-2xl bg-surface px-3 shadow-card ring-1 ring-fg/5 focus-within:ring-2 focus-within:ring-accent/50">
        <Search className="h-5 w-5 text-muted" aria-hidden />
        <input
          type="search"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t('catalog.search')}
          aria-label={t('catalog.search')}
          enterKeyHint="search"
          maxLength={100}
          className="h-12 min-w-0 flex-1 bg-transparent text-[16px] outline-none placeholder:text-muted"
        />
        {q && (
          <button
            type="button"
            onClick={() => {
              haptic('selection');
              setQ('');
            }}
            aria-label={t('catalog.clear')}
            className="grid h-8 w-8 place-items-center rounded-full text-muted transition hover:bg-fg/5 active:scale-90"
          >
            <X className="h-4 w-4" aria-hidden />
          </button>
        )}
      </label>

      <div className="no-scrollbar mt-3 flex gap-2 overflow-x-auto" role="radiogroup" aria-label={t('catalog.filters')}>
        {KINDS.map((k) => {
          const active = kind === k;
          return (
            <button
              key={k ?? 'all'}
              type="button"
              role="radio"
              aria-checked={active}
              onClick={() => {
                haptic('selection');
                setKind(k);
              }}
              className={cn(
                'min-h-[36px] shrink-0 rounded-full px-4 text-sm font-semibold transition active:scale-95',
                active ? 'bg-accent text-accent-fg' : 'bg-surface text-fg shadow-card',
              )}
            >
              {t(k ? `catalog.${k}` : 'catalog.all')}
            </button>
          );
        })}
      </div>

      {browsing && <ContinueRail />}

      <SectionHeader title={t('catalog.grid')} />
      {catalog.isPending && <TitleGridSkeleton />}
      {catalog.isError && (
        <ErrorState error={catalog.error} onRetry={() => void catalog.refetch()} retrying={catalog.isFetching} />
      )}
      {catalog.isSuccess && catalog.titles.length === 0 && (
        <EmptyState icon={<Film className="h-10 w-10 opacity-40" aria-hidden />}>
          {term ? t('catalog.noResults', { q: term }) : t('catalog.empty')}
        </EmptyState>
      )}
      {catalog.isSuccess && catalog.titles.length > 0 && (
        <>
          <TitleGrid titles={catalog.titles} />
          <LoadMore
            hasMore={catalog.hasNextPage}
            loading={catalog.isFetchingNextPage}
            onLoad={() => void catalog.fetchNextPage()}
          />
        </>
      )}
    </div>
  );
}
