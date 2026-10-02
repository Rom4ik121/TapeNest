import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { playlistsApi, tracksApi, type TrackListKind } from '@/shared/api/endpoints';
import { qk } from '@/shared/api/queryClient';
import type { Page, Track } from '@/shared/api/types';

const flatten = (pages: Page<Track>[] | undefined): Track[] => {
  const seen = new Set<string>();
  const out: Track[] = [];
  for (const p of pages ?? []) {
    for (const t of p.items) {
      if (seen.has(t.id)) continue;
      seen.add(t.id);
      out.push(t);
    }
  }
  return out;
};

/** Cursor-paginated track list: every page follows `nextCursor` (old bug: only page 1). */
export function useTrackList(kind: TrackListKind) {
  const q = useInfiniteQuery({
    queryKey: qk.tracks.list(kind),
    queryFn: ({ pageParam, signal }) => tracksApi.list(kind, pageParam, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.nextCursor,
  });
  return { ...q, tracks: flatten(q.data?.pages) };
}

export function useTrackSearch(query: string) {
  const term = query.trim();
  const q = useInfiniteQuery({
    queryKey: qk.tracks.search(term),
    queryFn: ({ pageParam, signal }) => tracksApi.search(term, pageParam, signal),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.nextCursor,
    enabled: term.length > 0,
    placeholderData: (prev) => prev,
  });
  return { ...q, tracks: flatten(q.data?.pages) };
}

export function usePlaylists() {
  return useQuery({ queryKey: qk.playlists.root, queryFn: playlistsApi.list });
}

export function usePlaylist(id: string) {
  return useQuery({
    queryKey: qk.playlists.detail(id),
    queryFn: () => playlistsApi.get(id),
  });
}

export function usePlaylistMutations() {
  const qc = useQueryClient();
  const invalidate = (): Promise<void> => qc.invalidateQueries({ queryKey: qk.playlists.root });
  return {
    create: useMutation({
      mutationFn: (title: string) => playlistsApi.create(title),
      onSuccess: invalidate,
    }),
    rename: useMutation({
      mutationFn: (v: { id: string; title: string }) => playlistsApi.rename(v.id, v.title),
      onSuccess: invalidate,
    }),
    remove: useMutation({
      mutationFn: (id: string) => playlistsApi.remove(id),
      onSuccess: invalidate,
    }),
    addTrack: useMutation({
      mutationFn: (v: { id: string; trackId: string }) => playlistsApi.addTrack(v.id, v.trackId),
      onSuccess: invalidate,
    }),
    removeTrack: useMutation({
      mutationFn: (v: { id: string; trackId: string }) => playlistsApi.removeTrack(v.id, v.trackId),
      onSuccess: invalidate,
    }),
  };
}
