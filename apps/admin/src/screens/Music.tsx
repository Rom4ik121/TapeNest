import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { ru } from '@/i18n/labels';
import { api } from '@/shared/api/client';
import { searchCatalog } from '@/shared/api/search';
import type { CatalogHealth } from '@/shared/api/types';
import { formatWhen } from '@/shared/format';
import { Empty, Failed, Loading } from '@/shared/ui/States';

export function Music() {
  const [draft, setDraft] = useState('');
  const [query, setQuery] = useState('');
  const catalog = useQuery({
    queryKey: ['admin-catalog'],
    queryFn: () => api<CatalogHealth>('/admin/catalog'),
  });
  const found = useQuery({
    queryKey: ['admin-search', query],
    queryFn: () => searchCatalog(query),
    enabled: query.length > 0,
  });

  return (
    <section className="space-y-4">
      <h1 className="text-2xl font-semibold">{ru.navMusic}</h1>
      <p className="text-sm text-muted">{ru.searchHint}</p>
      {catalog.isPending ? <Loading /> : null}
      {catalog.isError ? <Failed onRetry={() => void catalog.refetch()} /> : null}
      {catalog.data ? <CatalogStats catalog={catalog.data} /> : null}
      <form
        role="search"
        className="flex gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          setQuery(draft.trim());
        }}
      >
        <input
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          placeholder={ru.searchPlaceholder}
          type="search"
          className="min-w-0 flex-1 rounded-2xl border border-line/10 bg-surface px-4 py-3 text-sm outline-none focus:border-magenta"
          aria-label={ru.searchPlaceholder}
        />
        <button type="submit" className="rounded-2xl bg-accent px-4 py-3 text-sm font-semibold text-accent-fg">
          {ru.searchAction}
        </button>
      </form>
      {found.isPending && query ? <Loading /> : null}
      {found.isError ? <Failed onRetry={() => void found.refetch()} /> : null}
      {found.data && found.data.length === 0 ? <Empty /> : null}
      {found.data && found.data.length > 0 ? (
        <ul className="space-y-2">
          {found.data.map((track) => (
            <li key={track.id} className="rounded-2xl bg-surface px-4 py-3 shadow-card">
              <p className="font-medium">{track.title}</p>
              <p className="text-sm text-muted">
                {track.artist}
                {track.album ? ` · ${track.album}` : ''}
              </p>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}

function CatalogStats({ catalog }: { catalog: CatalogHealth }) {
  const cells: { label: string; value: number }[] = [
    { label: ru.tracks, value: catalog.tracks },
    { label: ru.albums, value: catalog.albums },
    { label: ru.artistsCount, value: catalog.artists },
    { label: ru.library, value: catalog.libraryTracks },
    { label: ru.youtube, value: catalog.youtubeTracks },
    { label: ru.deleted, value: catalog.deletedTracks },
  ];
  return (
    <div className="rounded-3xl bg-surface p-4 shadow-card">
      <h2 className="text-sm font-semibold uppercase tracking-wide text-muted">{ru.catalogTitle}</h2>
      <dl className="mt-3 grid grid-cols-3 gap-3">
        {cells.map((cell) => (
          <div key={cell.label}>
            <dt className="text-xs text-muted">{cell.label}</dt>
            <dd className="text-lg font-semibold tabular-nums">{cell.value}</dd>
          </div>
        ))}
      </dl>
      <p className="mt-3 text-xs text-muted">
        {ru.lastSync}: {formatWhen(catalog.lastSyncedAt)}
      </p>
    </div>
  );
}
