import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { qk } from '@/shared/api/queryClient';
import { downloadsApi } from './api';
import type { ComposeRequest } from './types';

export function useVideoList() {
  return useQuery({
    queryKey: qk.videos.list,
    queryFn: ({ signal }) => downloadsApi.list(null, signal),
    refetchInterval: (query) => {
      const items = query.state.data?.items ?? [];
      return items.some((item) => item.status === 'queued' || item.status === 'running') ? 2000 : false;
    },
  });
}

export function useVideo(id: string) {
  return useQuery({
    queryKey: qk.videos.one(id),
    queryFn: ({ signal }) => downloadsApi.get(id, signal),
    enabled: id.length > 0,
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === 'queued' || status === 'running' ? 2000 : false;
    },
  });
}

export function useVideoFile(id: string, enabled: boolean) {
  return useQuery({
    queryKey: qk.videos.file(id),
    queryFn: ({ signal }) => downloadsApi.fileUrl(id, signal),
    enabled,
    staleTime: 30 * 60 * 1000,
  });
}

export function useCreateDownload() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (url: string) => downloadsApi.create(url),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.videos.list }),
  });
}

export function useRenameVideo(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (title: string) => downloadsApi.rename(id, title),
    onSuccess: (job) => {
      qc.setQueryData(qk.videos.one(id), job);
      void qc.invalidateQueries({ queryKey: qk.videos.list });
    },
  });
}

export function useDeleteVideo() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => downloadsApi.remove(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.videos.list }),
  });
}

export function useComposeVideo() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: ComposeRequest) => downloadsApi.compose(body),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.videos.list }),
  });
}

export function useTrimVideo(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (range: { startSec: number; endSec: number }) => downloadsApi.trim(id, range.startSec, range.endSec),
    onSuccess: () => void qc.invalidateQueries({ queryKey: qk.videos.list }),
  });
}
