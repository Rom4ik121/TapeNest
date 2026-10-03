import { createStore, type StoreApi } from 'zustand/vanilla';
import type { StreamUrl, Track, WaveFeedbackAction, WaveMode, WaveSession, Page } from '@/shared/api/types';
import { audibleDuration, mediaRunsPastSong } from './audibleDuration';
import type { AudioEngineLike } from './audioEngine';
import { createListenTracker } from './listenTracker';
import { createPositionSaver, type SavePositionFn } from './positionSaver';
import type { StoredSession } from './session';

export type PlayerStatus =
  | 'idle' // nothing loaded
  | 'loading' // fetching stream URL / buffering start
  | 'playing'
  | 'paused'
  | 'blocked' // autoplay rejected by the WebView — needs a tap
  | 'error';

export interface PlayerState {
  queue: Track[];
  index: number;
  status: PlayerStatus;
  positionSec: number;
  durationSec: number;
  buffering: boolean;
  waveSessionId: string | null;
  waveFetching: boolean;
  /** Wave backend unavailable — playing popular instead. */
  waveFallback: boolean;
  /** Wave mood/mode requested from the recommender. */
  waveMode: WaveMode;
  /** Position to resume from on the next play of the current track (restored session). */
  resumeAtSec: number;
  fullPlayerOpen: boolean;
  /** Loop the current track instead of advancing when it ends. */
  repeatOne: boolean;
}

export interface PlayerActions {
  playQueue(tracks: Track[], startIndex?: number, waveSessionId?: string | null): void;
  playTrack(track: Track, context?: Track[]): void;
  /** Starts (or restarts) My Wave; `mode` defaults to the current waveMode. */
  startWave(mode?: WaveMode): Promise<void>;
  toggle(): void;
  next(auto?: boolean): Promise<void>;
  prev(): void;
  toggleRepeat(): void;
  seek(sec: number): void;
  setLiked(trackId: string, liked: boolean): void;
  toggleLikeCurrent(): void;
  openFullPlayer(open: boolean): void;
  restore(session: StoredSession): void;
  /** Force-save position (pause, hide, pagehide). */
  flushPosition(keepalive?: boolean): void;
}

export type PlayerStore = PlayerState & PlayerActions;

export interface PlayerDeps {
  engine: AudioEngineLike;
  streamUrl(trackId: string, signal: AbortSignal): Promise<StreamUrl>;
  like(trackId: string): Promise<unknown>;
  unlike(trackId: string): Promise<unknown>;
  reportListened(trackId: string, positionSec: number, completed: boolean): Promise<unknown>;
  /** Track left by the user before the listen event (≥50 %) — negative taste signal. */
  reportSkipped?(trackId: string, positionSec: number): Promise<unknown>;
  savePosition: SavePositionFn;
  waveStart(mode?: WaveMode): Promise<WaveSession>;
  waveMore(sessionId: string, afterTrackId: string): Promise<Track[]>;
  waveFeedback(sessionId: string, trackId: string, action: WaveFeedbackAction): Promise<unknown>;
  popular(): Promise<Page<Track>>;
  onLikeChanged?(trackId: string, liked: boolean): void;
  /** A track could not be started (e.g. no sources for a not-yet-available track). */
  onStreamError?(trackId: string, error: unknown): void;
  persistSession?(queue: Track[], index: number, positionSec: number): void;
  haptic?(kind: 'light' | 'success'): void;
  positionDebounceMs?: number;
  /**
   * Warm the following track (stream URL + first media bytes) so next and
   * auto-advance do not wait on a cold YouTube resolve.
   */
  warm?(trackId: string, signal: AbortSignal): Promise<void>;
}

const WAVE_PREFETCH_THRESHOLD = 5;
const MAX_CONSECUTIVE_FAILURES = 3;

const initialState: PlayerState = {
  queue: [],
  index: 0,
  status: 'idle',
  positionSec: 0,
  durationSec: 0,
  buffering: false,
  waveSessionId: null,
  waveFetching: false,
  waveFallback: false,
  waveMode: 'default',
  resumeAtSec: 0,
  fullPlayerOpen: false,
  repeatOne: false,
};

const noop = (): void => undefined;
const swallow = (p: Promise<unknown>): void => {
  p.catch(noop);
};

export function createPlayerStore(deps: PlayerDeps): StoreApi<PlayerStore> {
  const { engine } = deps;
  // Every load gets a token; results of older loads are discarded (fixes the
  // stale stream-url race when tracks are switched quickly).
  let loadToken = 0;
  let loadAbort: AbortController | null = null;
  let warmAbort: AbortController | null = null;
  let consecutiveFailures = 0;
  /** Natural end is in progress — ignore the pause it causes and a second ended. */
  let finishing = false;
  let finishCurrent = (): void => undefined;

  const tracker = createListenTracker((id, pos, completed) => swallow(deps.reportListened(id, pos, completed)));
  const saver = createPositionSaver(deps.savePosition, deps.positionDebounceMs ?? 5000);

  const store = createStore<PlayerStore>()((set, get) => {
    const current = (): Track | null => get().queue[get().index] ?? null;

    const persist = (): void => {
      const s = get();
      deps.persistSession?.(s.queue, s.index, s.positionSec);
    };

    /** Save the outgoing track before switching away from it. */
    const leaveCurrent = (): void => {
      const t = current();
      if (t && get().positionSec > 0) saver.update(t.id, get().positionSec);
      saver.flush();
    };

    const prefetchNext = (): void => {
      if (!deps.warm) return;
      warmAbort?.abort();
      const nxt = get().queue[get().index + 1];
      if (!nxt) {
        warmAbort = null;
        return;
      }
      const abort = new AbortController();
      warmAbort = abort;
      swallow(deps.warm(nxt.id, abort.signal));
    };

    /** Song finished (element ended, or the audible catalog length was reached). */
    finishCurrent = (): void => {
      if (finishing) return;
      const s = get();
      // The element pauses itself before `ended`, so a finished song is already
      // "paused" by the time we hear about it. A user pause never emits `ended`.
      if (s.status !== 'playing' && s.status !== 'paused') return;
      finishing = true;
      const track = s.queue[s.index];
      if (track) {
        tracker.complete(s.durationSec || track.durationSec);
        saver.update(track.id, 0);
        saver.flush();
      }
      // Finished → next time start from the beginning.
      set({ positionSec: 0 });
      engine.pause();
      if (get().repeatOne) {
        void startCurrent(0);
        return;
      }
      void get().next(true);
    };

    const startCurrent = async (startAtSec = 0): Promise<void> => {
      const track = current();
      if (!track) {
        finishing = false;
        return;
      }
      const token = ++loadToken;
      loadAbort?.abort();
      const abort = new AbortController();
      loadAbort = abort;

      tracker.begin(track.id);
      // Status first: the engine's 'pause' event must not be mistaken for a
      // user pause (it would save the old position under the new track id).
      set({
        status: 'loading',
        positionSec: startAtSec,
        durationSec: track.durationSec,
        resumeAtSec: 0,
      });
      finishing = false;
      engine.pause();
      persist();
      prefetchNext();

      let url: string;
      try {
        url = (await deps.streamUrl(track.id, abort.signal)).url;
      } catch (err) {
        if (token !== loadToken || abort.signal.aborted) return; // superseded
        deps.onStreamError?.(track.id, err);
        consecutiveFailures += 1;
        set({ status: 'error' });
        if (consecutiveFailures < MAX_CONSECUTIVE_FAILURES && get().index < get().queue.length - 1) {
          await get().next(true);
        }
        return;
      }
      if (token !== loadToken) return; // a newer track was requested meanwhile

      engine.load(url, startAtSec);
      const started = await engine.play();
      if (token !== loadToken) return;
      consecutiveFailures = 0;
      // isPlaying is driven by real media events; if play() was rejected
      // (autoplay policy) we show a "tap to play" state instead of lying.
      if (!started) set({ status: 'blocked' });
    };

    const extendWave = async (): Promise<boolean> => {
      const s = get();
      if (!s.waveSessionId || s.waveFetching) return false;
      const last = s.queue[s.queue.length - 1];
      if (!last) return false;
      const sessionId = s.waveSessionId;
      set({ waveFetching: true });
      try {
        const more = await deps.waveMore(sessionId, last.id);
        if (get().waveSessionId !== sessionId) return false; // queue replaced meanwhile
        set((st) => ({ queue: [...st.queue, ...more], waveFetching: false }));
        prefetchNext();
        return more.length > 0;
      } catch {
        set({ waveFetching: false });
        return false;
      }
    };

    return {
      ...initialState,

      playQueue(tracks, startIndex = 0, waveSessionId = null) {
        if (tracks.length === 0) return;
        leaveCurrent();
        consecutiveFailures = 0;
        set({
          queue: tracks,
          index: Math.min(Math.max(0, startIndex), tracks.length - 1),
          waveSessionId,
          waveFallback: false,
        });
        void startCurrent(0);
      },

      playTrack(track, context) {
        const list = context && context.length > 0 ? context : [track];
        const idx = list.findIndex((t) => t.id === track.id);
        if (idx < 0) get().playQueue([track, ...list], 0);
        else get().playQueue(list, idx);
      },

      async startWave(mode) {
        const m = mode ?? get().waveMode;
        set({ status: 'loading', waveMode: m });
        try {
          const session = await deps.waveStart(m);
          if (session.tracks.length === 0) throw new Error('empty wave');
          get().playQueue(session.tracks, 0, session.sessionId);
        } catch {
          // Graceful degradation: recommendations down → popular.
          try {
            const page = await deps.popular();
            get().playQueue(page.items, 0, null);
            set({ waveFallback: true });
          } catch {
            set({ status: 'error' });
          }
        }
      },

      toggle() {
        const s = get();
        if (!current()) return;
        if (s.status === 'playing') {
          engine.pause();
          return; // 'pause' event updates status + flushes position
        }
        if ((s.status === 'paused' || s.status === 'blocked') && s.resumeAtSec === 0 && engine.hasSource()) {
          void engine.play().then((ok) => {
            if (!ok) set({ status: 'blocked' });
          });
          return;
        }
        void startCurrent(s.resumeAtSec);
      },

      async next(auto = false) {
        const s = get();
        const cur = current();
        if (cur && s.waveSessionId && !auto) {
          swallow(deps.waveFeedback(s.waveSessionId, cur.id, 'skip'));
        }
        // Manual skip before the ≥50 % listen event: tell the recommender (any queue).
        if (cur && !auto && deps.reportSkipped && s.durationSec > 0 && s.positionSec < s.durationSec * 0.5) {
          swallow(deps.reportSkipped(cur.id, s.positionSec));
        }
        if (!auto) deps.haptic?.('light');
        leaveCurrent();

        const nextIndex = s.index + 1;
        if (nextIndex < s.queue.length) {
          set({ index: nextIndex });
          void startCurrent(0);
          if (s.waveSessionId && s.queue.length - nextIndex <= WAVE_PREFETCH_THRESHOLD) void extendWave();
          return;
        }
        if (s.waveSessionId && (await extendWave())) {
          set({ index: nextIndex });
          void startCurrent(0);
          return;
        }
        // End of a non-wave queue: stop at the end.
        engine.pause();
        set({ status: 'paused' });
        finishing = false;
      },

      prev() {
        const s = get();
        deps.haptic?.('light');
        if (s.positionSec > 3 || s.index === 0) {
          get().seek(0);
          return;
        }
        leaveCurrent();
        set({ index: s.index - 1 });
        void startCurrent(0);
      },

      toggleRepeat() {
        set((st) => ({ repeatOne: !st.repeatOne }));
      },

      seek(sec) {
        const s = get();
        const target = Math.max(0, Math.min(sec, s.durationSec || sec));
        if (engine.hasSource() && s.status !== 'idle' && s.resumeAtSec === 0) engine.seek(target);
        else set({ resumeAtSec: target });
        set({ positionSec: target });
        const t = current();
        if (t) saver.update(t.id, target);
      },

      setLiked(trackId, liked) {
        set((st) => ({
          queue: st.queue.map((t) => (t.id === trackId ? { ...t, liked } : t)),
        }));
        deps.onLikeChanged?.(trackId, liked);
        swallow(liked ? deps.like(trackId) : deps.unlike(trackId));
        const s = get();
        if (liked && s.waveSessionId && current()?.id === trackId) {
          swallow(deps.waveFeedback(s.waveSessionId, trackId, 'like'));
        }
        if (liked) deps.haptic?.('success');
      },

      toggleLikeCurrent() {
        const t = current();
        if (t) get().setLiked(t.id, !t.liked);
      },

      openFullPlayer(open) {
        set({ fullPlayerOpen: open });
      },

      restore(session) {
        if (get().queue.length > 0) return;
        const track = session.queue[session.index];
        if (!track) return;
        set({
          queue: session.queue,
          index: session.index,
          status: 'paused',
          positionSec: session.positionSec,
          durationSec: track.durationSec,
          resumeAtSec: session.positionSec,
        });
      },

      flushPosition(keepalive = false) {
        const t = current();
        const s = get();
        if (t && s.status !== 'idle' && s.positionSec > 0) saver.update(t.id, s.positionSec);
        saver.flush(keepalive);
        persist();
      },
    };
  });

  // ── engine → store ────────────────────────────────────────────────
  engine.on('playing', () => store.setState({ status: 'playing', buffering: false }));
  engine.on('waiting', () => store.setState({ buffering: true }));
  engine.on('pause', () => {
    if (finishing) return;
    const s = store.getState();
    if (s.status === 'playing') {
      store.setState({ status: 'paused' });
      s.flushPosition();
    }
  });
  engine.on('time', ({ positionSec, durationSec }) => {
    const s = store.getState();
    if (s.status === 'loading' && positionSec === 0) return;
    const track = s.queue[s.index];
    if (!track) return;
    const audible = audibleDuration(track.durationSec, durationSec);
    const pos = audible > 0 ? Math.min(positionSec, audible) : positionSec;
    // YouTube's container keeps moving after the song. End at the catalog length.
    if (s.status === 'playing' && mediaRunsPastSong(track.durationSec, durationSec) && positionSec >= track.durationSec) {
      store.setState({ positionSec: track.durationSec, durationSec: track.durationSec });
      finishCurrent();
      return;
    }
    store.setState({
      positionSec: pos,
      ...(audible > 0 ? { durationSec: audible } : {}),
    });
    if (s.status === 'playing') {
      tracker.progress(pos, audible || track.durationSec);
      saver.update(track.id, pos);
    }
  });
  engine.on('ended', () => {
    finishCurrent();
  });
  engine.on('error', () => {
    const s = store.getState();
    if (s.status === 'loading' || s.status === 'playing') {
      store.setState({ status: 'error' });
      if (s.index < s.queue.length - 1) void s.next(true);
    }
  });

  return store;
}
