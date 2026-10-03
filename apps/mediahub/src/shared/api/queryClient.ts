import { QueryClient } from '@tanstack/react-query';
import { ApiError } from './client';

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      refetchOnWindowFocus: false,
      retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 1,
    },
  },
});

export const qk = {
  downloads: ['mediahub', 'downloads'] as const,
  photos: ['mediahub', 'photos'] as const,
  project: (jobId: string) => ['mediahub', 'project', jobId] as const,
  photo: (id: string) => ['mediahub', 'photo', id] as const,
};
