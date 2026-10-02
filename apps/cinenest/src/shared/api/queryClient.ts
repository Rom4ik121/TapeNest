import { QueryClient } from '@tanstack/react-query';
import type { CatalogFilter } from './endpoints';
import { ApiError } from './client';

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      refetchOnWindowFocus: false,
      retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 1,
    },
  },
});

export const qk = {
  titles: (f: CatalogFilter) => ['cinema', 'titles', f.q.trim().toLowerCase(), f.kind ?? 'all'] as const,
  title: (id: string) => ['cinema', 'title', id] as const,
  continueWatching: ['cinema', 'continue'] as const,
  watchlist: ['cinema', 'watchlist'] as const,
  position: (titleId: string, fileId: string) => ['cinema', 'position', titleId, fileId] as const,
  stream: (id: string) => ['cinema', 'stream', id] as const,
} as const;
