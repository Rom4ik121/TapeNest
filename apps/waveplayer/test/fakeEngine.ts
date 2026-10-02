import type { AudioEngineLike, EngineEvent, EngineSnapshot } from '@/entities/player/audioEngine';

/** Deterministic AudioEngine double. */
export class FakeEngine implements AudioEngineLike {
  src: string | null = null;
  position = 0;
  duration = 0;
  paused = true;
  /** Result the next play() resolves with (false = autoplay blocked). */
  allowPlay = true;
  loads: string[] = [];
  private handlers = new Map<EngineEvent, Set<(s: EngineSnapshot) => void>>();

  emit(e: EngineEvent): void {
    this.handlers.get(e)?.forEach((h) => h(this.snapshot()));
  }
  load(url: string, startAtSec = 0): void {
    this.src = url;
    this.loads.push(url);
    this.position = startAtSec;
  }
  async play(): Promise<boolean> {
    if (!this.allowPlay) return false;
    this.paused = false;
    this.emit('playing');
    return true;
  }
  pause(): void {
    if (!this.paused) {
      this.paused = true;
      this.emit('pause');
    }
  }
  seek(sec: number): void {
    this.position = sec;
    this.emit('time');
  }
  setVolume(): void {}
  hasSource(): boolean {
    return this.src !== null;
  }
  snapshot(): EngineSnapshot {
    return { positionSec: this.position, durationSec: this.duration };
  }
  on(e: EngineEvent, h: (s: EngineSnapshot) => void): () => void {
    const set = this.handlers.get(e) ?? new Set();
    set.add(h);
    this.handlers.set(e, set);
    return () => set.delete(h);
  }
  /** Simulate playback progress. */
  tick(position: number, duration = this.duration): void {
    this.position = position;
    this.duration = duration;
    this.emit('time');
  }
}
