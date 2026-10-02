import { useInfiniteQuery, useMutation, useQuery, useQueryClient, type InfiniteData } from '@tanstack/react-query';
import { ApiError } from '@/shared/api/client';
import { cinemaApi, type CatalogFilter } from '@/shared/api/endpoints';
import { qk } from '@/shared/api/queryClient';
import type { Page, Title, TitleSummary } from '@/shared/api/types';

/** Flatten cursor pages, de-duplicating by id (a page boundary may shift under inserts). */
export function flattenPages<T extends { id: string }>(pages: Page<T>[] | undefined): T[] {
  const seen = new Set<string>();
  const out: T[] = [];
  for (const p of pages ?? []) {
    for (const it of p.items) {
      if (seen.has(it.id)) continue;
      seen.add(it.id);
      out.push(it);
    }
  }
  return out;
}

export function useCatalog(filter: CatalogFilter) {
  const q = useInfiniteQuery({
    queryKey: qk.titles(filter),
    queryFn: ({ pageParam, signal }) => cinemaApi.titles(filter, pageParam, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.nextCursor,
    placeholderData: (prev) => prev,
  });
  return { ...q, titles: flattenPages(q.data?.pages) };
}

export function useWatchlist() {
  const q = useInfiniteQuery({
    queryKey: qk.watchlist,
    queryFn: ({ pageParam, signal }) => cinemaApi.watchlist(pageParam, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.nextCursor,
  });
  return { ...q, titles: flattenPages(q.data?.pages) };
}

export function useTitle(id: string) {
  return useQuery({ queryKey: qk.title(id), queryFn: ({ signal }) => cinemaApi.title(id, signal) });
}

export function useContinueWatching() {
  return useQuery({
    queryKey: qk.continueWatching,
    queryFn: ({ signal }) => cinemaApi.continueWatching(signal),
    select: (p) => p.items,
  });
}

/** Saved position; 404 = never watched → null (not an error). */
export function usePosition(titleId: string, fileId: string | null) {
  return useQuery({
    queryKey: qk.position(titleId, fileId ?? ''),
    enabled: !!fileId,
    queryFn: async ({ signal }) => {
      try {
        return await cinemaApi.getPosition(titleId, fileId ?? '', signal);
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }
    },
    staleTime: 0,
  });
}

const patchSummaries = (
  data: InfiniteData<Page<TitleSummary>> | undefined,
  id: string,
  on: boolean,
): InfiniteData<Page<TitleSummary>> | undefined =>
  data && {
    ...data,
    pages: data.pages.map((p) => ({ ...p, items: p.items.map((t) => (t.id === id ? { ...t, inWatchlist: on } : t)) })),
  };

/** "Watch later" toggle with optimistic update of the card and catalog; rollback on error. */
export function useToggleWatchlist() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, on }: { id: string; on: boolean }) => cinemaApi.setWatchlist(id, on),
    onMutate: async ({ id, on }) => {
      await qc.cancelQueries({ queryKey: qk.title(id) });
      const prevTitle = qc.getQueryData<Title>(qk.title(id));
      if (prevTitle) qc.setQueryData<Title>(qk.title(id), { ...prevTitle, inWatchlist: on });
      const catalogs = qc.getQueriesData<InfiniteData<Page<TitleSummary>>>({ queryKey: ['cinema', 'titles'] });
      for (const [key, data] of catalogs) qc.setQueryData(key, patchSummaries(data, id, on));
      return { prevTitle, catalogs };
    },
    onError: (_e, { id }, ctx) => {
      if (ctx?.prevTitle) qc.setQueryData(qk.title(id), ctx.prevTitle);
      for (const [key, data] of ctx?.catalogs ?? []) qc.setQueryData(key, data);
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: qk.watchlist }),
  });
}
