import { useQuery } from '@tanstack/react-query';
import { catalogApi } from '@/shared/api/endpoints';
import { qk } from '@/shared/api/queryClient';

/** Unified search: library + external catalog in one response. */
export function useCatalogSearch(query: string) {
  const term = query.trim();
  return useQuery({
    queryKey: qk.catalog.search(term),
    queryFn: ({ signal }) => catalogApi.search(term, signal),
    enabled: term.length > 0,
    placeholderData: (prev) => prev,
    staleTime: 60_000,
  });
}

export function useAlbum(id: string) {
  return useQuery({ queryKey: qk.catalog.album(id), queryFn: ({ signal }) => catalogApi.album(id, signal) });
}

export function useArtist(id: string) {
  return useQuery({ queryKey: qk.catalog.artist(id), queryFn: ({ signal }) => catalogApi.artist(id, signal) });
}
