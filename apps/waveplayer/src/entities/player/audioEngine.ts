/**
 * Owner of the single HTMLAudioElement (spec §3.1 #13: MVP = HTMLAudioElement).
 * Lives outside React so playback survives navigation. The store never reads
 * the element directly — it reacts to engine events.
 */
export type EngineEvent = 'playing' | 'pause' | 'ended' | 'error' | 'waiting' | 'time';

export interface EngineSnapshot {
  positionSec: number;
  durationSec: number;
}

export interface AudioEngineLike {
  load(url: string, startAtSec?: number): void;
  /** Resolves true if playback actually started, false if blocked (autoplay policy) or failed. */
  play(): Promise<boolean>;
  pause(): void;
  seek(sec: number): void;
  setVolume(v: number): void;
  hasSource(): boolean;
  snapshot(): EngineSnapshot;
  on(event: EngineEvent, handler: (snap: EngineSnapshot) => void): () => void;
}

export class HtmlAudioEngine implements AudioEngineLike {
  private el: HTMLAudioElement | null = null;
  private readonly handlers = new Map<EngineEvent, Set<(s: EngineSnapshot) => void>>();
  private pendingSeek: number | null = null;

  private audio(): HTMLAudioElement {
    if (this.el) return this.el;
    const a = new Audio();
    a.preload = 'auto';
    a.setAttribute('playsinline', '');
    const emit = (e: EngineEvent) => () => this.emit(e);
    a.addEventListener('playing', emit('playing'));
    a.addEventListener('pause', emit('pause'));
    a.addEventListener('ended', emit('ended'));
    a.addEventListener('error', emit('error'));
    a.addEventListener('waiting', emit('waiting'));
    a.addEventListener('timeupdate', emit('time'));
    a.addEventListener('loadedmetadata', () => {
      if (this.pendingSeek !== null) {
        const target = this.pendingSeek;
        this.pendingSeek = null;
        if (Number.isFinite(a.duration) && target < a.duration - 1) a.currentTime = target;
      }
      this.emit('time');
    });
    this.el = a;
    return a;
  }

  private emit(event: EngineEvent): void {
    const snap = this.snapshot();
    this.handlers.get(event)?.forEach((h) => h(snap));
  }

  load(url: string, startAtSec = 0): void {
    const a = this.audio();
    this.pendingSeek = startAtSec > 0 ? startAtSec : null;
    a.src = url;
    a.load();
  }

  async play(): Promise<boolean> {
    try {
      await this.audio().play();
      return true;
    } catch {
      // NotAllowedError (autoplay without gesture) or aborted by a newer load().
      return false;
    }
  }

  pause(): void {
    this.el?.pause();
  }

  seek(sec: number): void {
    const a = this.audio();
    const max = Number.isFinite(a.duration) ? a.duration : sec;
    a.currentTime = Math.min(Math.max(0, sec), Math.max(0, max));
    this.emit('time');
  }

  setVolume(v: number): void {
    this.audio().volume = Math.min(1, Math.max(0, v));
  }

  hasSource(): boolean {
    return Boolean(this.el?.currentSrc || this.el?.src);
  }

  snapshot(): EngineSnapshot {
    const a = this.el;
    if (!a) return { positionSec: 0, durationSec: 0 };
    return {
      positionSec: a.currentTime || 0,
      durationSec: Number.isFinite(a.duration) ? a.duration : 0,
    };
  }

  on(event: EngineEvent, handler: (snap: EngineSnapshot) => void): () => void {
    let set = this.handlers.get(event);
    if (!set) {
      set = new Set();
      this.handlers.set(event, set);
    }
    set.add(handler);
    return () => set.delete(handler);
  }
}
