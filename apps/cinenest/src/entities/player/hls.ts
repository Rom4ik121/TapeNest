import Hls from 'hls.js';

export type HlsFatal = 'network' | 'media' | 'other';

export interface AttachOptions {
  /** Access token for same-origin HLS requests (streaming-service checks it). */
  getToken: () => string | null;
  startAt?: number;
  onFatal?: (kind: HlsFatal) => void;
}

/** Same-origin (our domain) → may carry the Bearer token; foreign hosts never get it. */
export function isSameOrigin(url: string, origin = window.location.origin): boolean {
  try {
    return new URL(url, origin).origin === origin;
  } catch {
    return false;
  }
}

/**
 * Attach an HLS source (hls.js 1.5; native HLS on iOS/Safari where MSE is missing).
 * Returns a destroy function. Native HLS cannot send headers — streaming-service
 * accepts a short-lived signed `token` query parameter in hlsUrl for that case (stage 4).
 */
export function attachHls(video: HTMLVideoElement, url: string, opts: AttachOptions): () => void {
  const start = opts.startAt && opts.startAt > 0 ? opts.startAt : 0;
  if (Hls.isSupported()) {
    const hls = new Hls({
      startPosition: start || -1,
      maxBufferLength: 30,
      xhrSetup: (xhr, reqUrl) => {
        const token = opts.getToken();
        if (token && isSameOrigin(reqUrl)) xhr.setRequestHeader('Authorization', `Bearer ${token}`);
      },
    });
    hls.on(Hls.Events.ERROR, (_e, data) => {
      if (!data.fatal) return;
      if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
        opts.onFatal?.('network');
        hls.startLoad(); // one recovery attempt per fatal network error
      } else if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
        opts.onFatal?.('media');
        hls.recoverMediaError();
      } else {
        opts.onFatal?.('other');
        hls.destroy();
      }
    });
    hls.loadSource(url);
    hls.attachMedia(video);
    return () => hls.destroy();
  }
  if (video.canPlayType('application/vnd.apple.mpegurl')) {
    const onMeta = (): void => {
      if (start) video.currentTime = start;
    };
    video.addEventListener('loadedmetadata', onMeta, { once: true });
    video.src = url;
    return () => {
      video.removeEventListener('loadedmetadata', onMeta);
      video.removeAttribute('src');
      video.load();
    };
  }
  opts.onFatal?.('other');
  return () => undefined;
}
