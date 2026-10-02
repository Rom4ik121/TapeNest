import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { cinemaApi } from '@/shared/api/endpoints';
import { qk } from '@/shared/api/queryClient';
import type { StreamSession } from '@/shared/api/types';
import { streamPollInterval, warmupView, type WarmupView } from './warmup';

/**
 * Start (POST /cinema/streams) and poll a stream session until it is ready/failed.
 * The session is stopped (DELETE) when the player unmounts or the file changes.
 */
export function useStream(titleId: string, fileId: string) {
  const qc = useQueryClient();
  const [attempt, setAttempt] = useState(0);
  const start = useQuery({
    queryKey: ['cinema', 'start', titleId, fileId, attempt] as const,
    queryFn: () => cinemaApi.startStream(titleId, fileId),
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
  });
  const id = start.data?.id ?? null;
  const poll = useQuery({
    queryKey: qk.stream(id ?? ''),
    queryFn: ({ signal }) => cinemaApi.stream(id ?? '', signal),
    enabled: !!id,
    initialData: start.data,
    refetchInterval: (q) => streamPollInterval(q.state.data),
    gcTime: 0,
  });

  useEffect(() => {
    if (!id) return;
    return () => {
      void cinemaApi.stopStream(id).catch(() => undefined);
      qc.removeQueries({ queryKey: qk.stream(id) });
    };
  }, [id, qc]);

  const session: StreamSession | undefined = poll.data ?? start.data;
  const prev = useRef<{ pct: number; at: number } | undefined>(undefined);
  const view: WarmupView = start.isError
    ? { phase: 'failed', pct: 0, etaSec: null }
    : warmupView(session, prev.current);
  useEffect(() => {
    if (session?.status === 'warming') prev.current ??= { pct: session.bufferedPct, at: Date.now() };
    else prev.current = undefined;
  }, [session?.status, session?.bufferedPct]);

  return {
    session,
    view,
    error: start.error ?? poll.error,
    retry: () => {
      prev.current = undefined;
      setAttempt((n) => n + 1);
    },
  };
}
