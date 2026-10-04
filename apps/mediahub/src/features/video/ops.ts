export interface Crop {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface VideoClip {
  startSec: number;
  endSec: number;
  speed: number;
  volume: number;
  crop: Crop;
  rotate: 0 | 90 | 180 | 270;
  transition: string;
  transitionSec: number;
}

export const TRANSITIONS = ['none', 'fade', 'wipeleft', 'wiperight', 'slideleft', 'slideright', 'circleopen', 'fadeblack'] as const;

export function fullCrop(): Crop {
  return { x: 0, y: 0, w: 1, h: 1 };
}

export function splitClip(clips: VideoClip[], index: number): VideoClip[] {
  const clip = clips[index];
  if (!clip) return clips;
  const mid = (clip.startSec + clip.endSec) / 2;
  if (mid - clip.startSec < 0.2 || clip.endSec - mid < 0.2) return clips;
  const left: VideoClip = { ...clip, endSec: Number(mid.toFixed(2)) };
  const right: VideoClip = { ...clip, startSec: Number(mid.toFixed(2)), transition: 'fade', transitionSec: 0.4 };
  return [...clips.slice(0, index), left, right, ...clips.slice(index + 1)];
}

export function moveClip(clips: VideoClip[], index: number, dir: -1 | 1): VideoClip[] {
  const next = index + dir;
  if (next < 0 || next >= clips.length) return clips;
  const copy = clips.slice();
  const a = copy[index];
  const b = copy[next];
  if (!a || !b) return clips;
  copy[index] = b;
  copy[next] = a;
  return copy;
}

export function removeClip(clips: VideoClip[], index: number): VideoClip[] {
  if (clips.length <= 1) return clips;
  return clips.filter((_, i) => i !== index);
}
