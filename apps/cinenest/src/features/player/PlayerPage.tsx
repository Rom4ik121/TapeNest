import { useQueryClient } from '@tanstack/react-query';
import { ChevronDown, Maximize, SkipForward } from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { useNavigate, useParams } from 'react-router-dom';
import { useAuthStore } from '@/entities/auth/authStore';
import { attachHls } from '@/entities/player/hls';
import { createPositionSaver } from '@/entities/player/positionSaver';
import { useStream } from '@/entities/player/useStream';
import { nextEpisode, resumeAt } from '@/entities/title/files';
import { usePosition, useTitle } from '@/entities/title/queries';
import { cinemaApi } from '@/shared/api/endpoints';
import { qk } from '@/shared/api/queryClient';
import { formatTime } from '@/shared/lib/format';
import { haptic, toggleTelegramFullscreen } from '@/shared/telegram';
import { WarmupOverlay } from './WarmupOverlay';

/**
 * Movie player (spec §6): hls.js 1.5 + TorrServer warm-up indicator + resume from
 * the saved position + fullscreen. Position: debounce 5 s + forced flush on pause,
 * hide/pagehide and close.
 */
export function PlayerPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { titleId = '', fileId = '' } = useParams();
  const title = useTitle(titleId);
  const pos = usePosition(titleId, fileId);
  const { session, view, retry } = useStream(titleId, fileId);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const boxRef = useRef<HTMLDivElement | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [ended, setEnded] = useState(false);
  const [mediaError, setMediaError] = useState(false);

  const file = title.data?.files.find((f) => f.id === fileId) ?? null;
  const next = useMemo(() => (title.data && file ? nextEpisode(title.data.files, file) : null), [title.data, file]);
  const hlsUrl = session?.status === 'ready' ? session.hlsUrl : null;
  const startAt = pos.isSuccess ? resumeAt(pos.data) : null;

  const saver = useMemo(
    () =>
      createPositionSaver((s, keepalive) =>
        cinemaApi.savePosition(s.titleId, s.fileId, s.positionSec, s.durationSec, keepalive),
      ),
    [],
  );

  // Attach the stream once it is ready and the saved position is known.
  useEffect(() => {
    const video = videoRef.current;
    if (!video || !hlsUrl || startAt === null) return;
    setEnded(false);
    const destroy = attachHls(video, hlsUrl, {
      getToken: () => useAuthStore.getState().accessToken,
      startAt,
      onFatal: (kind) => setMediaError(kind !== 'network'),
    });
    if (startAt > 0) {
      setToast(t('player.resumed', { time: formatTime(startAt) }));
      const id = setTimeout(() => setToast(null), 3000);
      void video.play().catch(() => undefined);
      return () => {
        clearTimeout(id);
        destroy();
      };
    }
    void video.play().catch(() => undefined);
    return destroy;
  }, [hlsUrl, startAt, t]);

  // Position persistence.
  useEffect(() => {
    const video = videoRef.current;
    if (!video) return;
    const sample = () => ({ titleId, fileId, positionSec: video.currentTime, durationSec: video.duration || 0 });
    const onTime = (): void => {
      if (!video.paused) saver.update(sample());
    };
    const onPause = (): void => {
      saver.update(sample());
      saver.flush();
    };
    const onHide = (): void => {
      if (document.visibilityState === 'hidden') {
        saver.update(sample());
        saver.flush(true);
      }
    };
    const onPageHide = (): void => {
      saver.update(sample());
      saver.flush(true);
    };
    const onEnded = (): void => {
      setEnded(true);
      saver.update(sample());
      saver.flush();
    };
    video.addEventListener('timeupdate', onTime);
    video.addEventListener('pause', onPause);
    video.addEventListener('ended', onEnded);
    document.addEventListener('visibilitychange', onHide);
    window.addEventListener('pagehide', onPageHide);
    return () => {
      if (video.currentTime > 0) saver.update(sample());
      saver.flush(true);
      saver.dispose();
      video.removeEventListener('timeupdate', onTime);
      video.removeEventListener('pause', onPause);
      video.removeEventListener('ended', onEnded);
      document.removeEventListener('visibilitychange', onHide);
      window.removeEventListener('pagehide', onPageHide);
      void qc.invalidateQueries({ queryKey: qk.continueWatching });
      void qc.invalidateQueries({ queryKey: qk.position(titleId, fileId) });
    };
  }, [saver, titleId, fileId, qc]);

  const fullscreen = (): void => {
    haptic('light');
    if (toggleTelegramFullscreen()) return;
    const el = boxRef.current;
    if (!el) return;
    if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined);
    else if (el.requestFullscreen) void el.requestFullscreen().catch(() => undefined);
    else
      (
        videoRef.current as (HTMLVideoElement & { webkitEnterFullscreen?: () => void }) | null
      )?.webkitEnterFullscreen?.();
  };

  const heading = [
    title.data?.title,
    file?.season !== null && file?.episode !== null && file
      ? t('catalog.episodeShort', { s: file.season, e: file.episode })
      : null,
  ]
    .filter(Boolean)
    .join(' · ');

  // Portal: the route wrapper animates with transform, which would turn `fixed` into
  // "relative to the page" and break the fullscreen overlay.
  return createPortal(
    <div
      className="fixed inset-0 z-50 flex flex-col bg-ink text-cream"
      role="dialog"
      aria-modal="true"
      aria-label={t('player.title')}
    >
      <header className="relative z-10 flex items-center gap-2 px-3 pb-2 pt-[calc(var(--app-safe-top)+0.5rem)]">
        <button
          type="button"
          onClick={() => navigate(-1)}
          aria-label={t('player.close')}
          className="grid h-10 w-10 place-items-center rounded-full bg-cream/10 transition active:scale-90"
        >
          <ChevronDown className="h-5 w-5" aria-hidden />
        </button>
        <p className="min-w-0 flex-1 truncate text-center text-sm font-semibold">{heading}</p>
        <button
          type="button"
          onClick={fullscreen}
          aria-label={t('player.fullscreen')}
          className="grid h-10 w-10 place-items-center rounded-full bg-cream/10 transition active:scale-90"
        >
          <Maximize className="h-5 w-5" aria-hidden />
        </button>
      </header>
      <div ref={boxRef} className="relative flex flex-1 items-center justify-center bg-black">
        <video
          ref={videoRef}
          className="max-h-full w-full"
          controls={view.phase === 'ready'}
          playsInline
          preload="metadata"
          aria-label={heading || t('player.title')}
        />
        <WarmupOverlay view={view} session={session} onRetry={retry} />
        {toast && (
          <div
            role="status"
            className="page-enter absolute left-1/2 top-4 -translate-x-1/2 rounded-full bg-ink/80 px-4 py-2 text-sm font-semibold backdrop-blur"
          >
            {toast}
          </div>
        )}
        {mediaError && view.phase === 'ready' && (
          <div
            role="alert"
            className="absolute bottom-16 left-1/2 -translate-x-1/2 rounded-full bg-ink/80 px-4 py-2 text-sm"
          >
            {t('player.mediaError')}
          </div>
        )}
        {ended && next && (
          <button
            type="button"
            onClick={() => {
              haptic('medium');
              navigate(`/watch/${encodeURIComponent(titleId)}/${encodeURIComponent(next.id)}`, { replace: true });
            }}
            className="page-enter absolute bottom-20 right-4 inline-flex min-h-[44px] items-center gap-2 rounded-full bg-amber px-5 font-bold text-ink shadow-glow"
          >
            <SkipForward className="h-4 w-4 fill-current" aria-hidden /> {t('player.nextEpisode')}
          </button>
        )}
      </div>
      <div className="pb-safe" />
    </div>,
    document.body,
  );
}
