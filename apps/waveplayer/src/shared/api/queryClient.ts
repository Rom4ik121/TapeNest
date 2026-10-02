import { QueryClient } from '@tanstack/react-query';
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
  tracks: {
    root: ['tracks'] as const,
    list: (kind: string) => ['tracks', 'list', kind] as const,
    search: (q: string) => ['tracks', 'search', q] as const,
  },
  catalog: {
    root: ['catalog'] as const,
    search: (q: string) => ['catalog', 'search', q] as const,
    album: (id: string) => ['catalog', 'album', id] as const,
    artist: (id: string) => ['catalog', 'artist', id] as const,
  },
  playlists: {
    root: ['playlists'] as const,
    detail: (id: string) => ['playlists', id] as const,
  },
} as const;
