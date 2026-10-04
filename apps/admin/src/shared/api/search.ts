import { ApiError, api } from '@/shared/api/client';
import type { SearchTrack } from '@/shared/api/types';

interface UnifiedSearch {
  tracks?: SearchTrack[];
}

interface LibrarySearch {
  items?: SearchTrack[];
}

/** Existing catalog search. Unified discovery first, library search when it is off. */
export async function searchCatalog(q: string): Promise<SearchTrack[]> {
  try {
    const unified = await api<UnifiedSearch>('/search', { query: { q } });
    return unified.tracks ?? [];
  } catch (err) {
    if (err instanceof ApiError && (err.status === 404 || err.status === 501)) {
      const page = await api<LibrarySearch>('/tracks/search', { query: { q } });
      return page.items ?? [];
    }
    throw err;
  }
}
