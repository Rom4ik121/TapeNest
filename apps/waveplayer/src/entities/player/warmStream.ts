import { tracksApi } from '@/shared/api/endpoints';

/**
 * Asks for the next track's stream URL, then reads the first bytes.
 * stream-url itself returns before yt-dlp runs; the signed media URL is what
 * resolves and caches the YouTube stream for the single audio element.
 */
export async function warmStream(trackId: string, signal: AbortSignal): Promise<void> {
  const { url } = await tracksApi.streamUrl(trackId, signal);
  if (signal.aborted) return;
  const res = await fetch(url, {
    method: 'GET',
    headers: { Range: 'bytes=0-1' },
    signal,
  });
  await res.body?.cancel();
}
