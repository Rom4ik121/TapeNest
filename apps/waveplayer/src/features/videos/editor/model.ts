/**
 * Timeline for the owner's own downloads.
 * Join math matches services/download-service/internal/service/compose.go:
 * a fade or wipe overlaps the previous clip by min(requested, 45% of either side),
 * clamped to 0.2–1.5s. A hard cut overlaps by nothing.
 */

import type { ComposeRequest, EditRotate, EditTransition } from '@/entities/video/types';

export const SPEEDS = [0.5, 1, 1.5, 2] as const;
export const ROTATIONS = [0, 90, 180, 270] as const;
export const MIN_SOURCE = 0.5;
export const MIN_OUTPUT = 0.5;
export const MAX_CLIPS = 8;
export const MAX_TEXTS = 8;

export type Speed = (typeof SPEEDS)[number];
export type Rotate = EditRotate;
export type TransitionKind = EditTransition;

export interface Crop {
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface TimelineClip {
  id: string;
  jobId: string;
  title: string;
  posterUrl?: string;
  sourceDuration: number;
  inSec: number;
  outSec: number;
  speed: Speed;
  volume: number;
  crop: Crop;
  rotate: Rotate;
  /** How this clip arrives from the previous one. Ignored on the first clip. */
  transition: TransitionKind;
  transitionSec: number;
}

export interface TextCue {
  id: string;
  text: string;
  startSec: number;
  endSec: number;
  x: number;
  y: number;
}

export interface MusicTrack {
  jobId: string;
  title: string;
  inSec: number;
  volume: number;
  offsetSec: number;
}

export interface Project {
  clips: TimelineClip[];
  texts: TextCue[];
  music: MusicTrack | null;
}

export interface History {
  past: Project[];
  present: Project;
  future: Project[];
}

export type { ComposeRequest };

export interface Span {
  id: string;
  start: number;
  end: number;
}

export interface Hit {
  index: number;
  sourceSec: number;
}

export interface Place {
  clip: Hit;
  incoming: Hit | null;
  mix: number;
}

const HISTORY_LIMIT = 40;

export function uid(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID();
  return `id-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`;
}

export function fullCrop(): Crop {
  return { x: 0, y: 0, w: 1, h: 1 };
}

function round3(n: number): number {
  return Math.round(n * 1000) / 1000;
}

function clamp(n: number, min: number, max: number): number {
  if (!Number.isFinite(n)) return min;
  return Math.min(max, Math.max(min, n));
}

export function clampVolume(n: number): number {
  return clamp(n, 0, 1.5);
}

export function asSpeed(n: number): Speed | null {
  return SPEEDS.find((speed) => Math.abs(speed - n) < 0.001) ?? null;
}

function minSource(speed: number): number {
  return Math.max(MIN_SOURCE, MIN_OUTPUT * speed);
}

export function outputSeconds(clip: TimelineClip): number {
  return Math.max(0, (clip.outSec - clip.inSec) / clip.speed);
}

/** Overlap placed on `next` (0 for a hard cut or the first clip). */
export function joinSeconds(prev: TimelineClip, next: TimelineClip): number {
  if (next.transition === 'none') return 0;
  let td = next.transitionSec <= 0 ? 0.5 : next.transitionSec;
  td = Math.min(Math.max(td, 0.2), 1.5);
  const cap = Math.min(outputSeconds(prev), outputSeconds(next)) * 0.45;
  td = Math.min(td, cap);
  if (td < 0.05) return 0;
  return round3(td);
}

export function spans(project: Project): Span[] {
  const out: Span[] = [];
  let cursor = 0;
  project.clips.forEach((clip, index) => {
    const prev = index > 0 ? project.clips[index - 1] : undefined;
    const join = prev ? joinSeconds(prev, clip) : 0;
    const start = cursor - join;
    const end = start + outputSeconds(clip);
    out.push({ id: clip.id, start: round3(start), end: round3(end) });
    cursor = end;
  });
  return out;
}

export function projectDuration(project: Project): number {
  const all = spans(project);
  return all[all.length - 1]?.end ?? 0;
}

export function placeAt(project: Project, time: number): Place | null {
  const all = spans(project);
  if (all.length === 0) return null;
  const total = all[all.length - 1]?.end ?? 0;
  const t = clamp(time, 0, total);
  let index = all.length - 1;
  for (let i = 0; i < all.length; i += 1) {
    const span = all[i];
    if (!span) continue;
    if (t < span.end - 1e-6 || i === all.length - 1) {
      index = i;
      break;
    }
  }
  const span = all[index];
  const clip = project.clips[index];
  if (!span || !clip) return null;
  const into = Math.max(0, t - span.start);
  const primary: Hit = { index, sourceSec: round3(clip.inSec + into * clip.speed) };
  const nextSpan = all[index + 1];
  const nextClip = project.clips[index + 1];
  if (nextSpan && nextClip && t > nextSpan.start + 1e-6 && t < span.end - 1e-6) {
    const join = span.end - nextSpan.start;
    if (join > 0.05) {
      const intoNext = Math.max(0, t - nextSpan.start);
      return {
        clip: primary,
        incoming: { index: index + 1, sourceSec: round3(nextClip.inSec + intoNext * nextClip.speed) },
        mix: round3(clamp(intoNext / join, 0, 1)),
      };
    }
  }
  return { clip: primary, incoming: null, mix: 0 };
}

export function clipFromJob(
  job: { id: string; title?: string; posterUrl?: string; file?: { durationSec: number } },
  id: string,
): TimelineClip | null {
  const duration = job.file?.durationSec ?? 0;
  if (duration < MIN_SOURCE) return null;
  return {
    id,
    jobId: job.id,
    title: job.title?.trim() || '',
    posterUrl: job.posterUrl,
    sourceDuration: duration,
    inSec: 0,
    outSec: duration,
    speed: 1,
    volume: 1,
    crop: fullCrop(),
    rotate: 0,
    transition: 'none',
    transitionSec: 0.5,
  };
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function same(a: Project, b: Project): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

export function emptyHistory(present: Project): History {
  return { past: [], present, future: [] };
}

export function commit(history: History, present: Project): History {
  if (same(history.present, present)) return history;
  return { past: [...history.past, history.present].slice(-HISTORY_LIMIT), present, future: [] };
}

export function undo(history: History): History {
  const prev = history.past[history.past.length - 1];
  if (!prev) return history;
  return { past: history.past.slice(0, -1), present: prev, future: [history.present, ...history.future] };
}

export function redo(history: History): History {
  const next = history.future[0];
  if (!next) return history;
  return {
    past: [...history.past, history.present].slice(-HISTORY_LIMIT),
    present: next,
    future: history.future.slice(1),
  };
}

export function splitClip(
  project: Project,
  clipId: string,
  sourceSec: number,
  rightId: string,
): { project: Project; rightId: string } | null {
  const next = clone(project);
  const index = next.clips.findIndex((clip) => clip.id === clipId);
  const clip = next.clips[index];
  if (!clip) return null;
  const need = minSource(clip.speed);
  if (sourceSec < clip.inSec + need - 1e-6 || sourceSec > clip.outSec - need + 1e-6) return null;
  const right: TimelineClip = { ...clip, id: rightId, inSec: round3(sourceSec), transition: 'none' };
  clip.outSec = round3(sourceSec);
  next.clips.splice(index + 1, 0, right);
  return { project: next, rightId };
}

export function trimClip(project: Project, clipId: string, edge: 'in' | 'out', sourceSec: number): Project {
  const next = clone(project);
  const clip = next.clips.find((item) => item.id === clipId);
  if (!clip || !Number.isFinite(sourceSec)) return project;
  const need = minSource(clip.speed);
  if (edge === 'in') clip.inSec = round3(clamp(sourceSec, 0, clip.outSec - need));
  else clip.outSec = round3(clamp(sourceSec, clip.inSec + need, clip.sourceDuration));
  return next;
}

export function moveClip(project: Project, clipId: string, direction: -1 | 1): Project {
  const next = clone(project);
  const index = next.clips.findIndex((clip) => clip.id === clipId);
  const target = index + direction;
  if (index < 0 || target < 0 || target >= next.clips.length) return project;
  const [item] = next.clips.splice(index, 1);
  if (!item) return project;
  next.clips.splice(target, 0, item);
  return next;
}

export interface ClipPatch {
  speed?: number;
  volume?: number;
  crop?: Crop;
  rotate?: Rotate;
  transition?: TransitionKind;
  transitionSec?: number;
}

function validCrop(crop: Crop): boolean {
  const nums = [crop.x, crop.y, crop.w, crop.h];
  if (!nums.every((n) => Number.isFinite(n))) return false;
  if (crop.x < -0.001 || crop.y < -0.001 || crop.w < 0.1 || crop.h < 0.1) return false;
  return crop.x + crop.w <= 1.001 && crop.y + crop.h <= 1.001;
}

function isTransition(value: string): value is TransitionKind {
  return value === 'none' || value === 'fade' || value === 'wipe';
}

export function patchClip(project: Project, clipId: string, patch: ClipPatch): Project | null {
  const next = clone(project);
  const clip = next.clips.find((item) => item.id === clipId);
  if (!clip) return null;
  if (patch.speed !== undefined) {
    const speed = asSpeed(patch.speed);
    if (!speed) return null;
    if ((clip.outSec - clip.inSec) / speed < MIN_OUTPUT - 1e-6) return null;
    clip.speed = speed;
  }
  if (patch.volume !== undefined) clip.volume = round3(clampVolume(patch.volume));
  if (patch.rotate !== undefined) {
    if (!(ROTATIONS as readonly number[]).includes(patch.rotate)) return null;
    clip.rotate = patch.rotate;
  }
  if (patch.crop !== undefined) {
    if (!validCrop(patch.crop)) return null;
    clip.crop = { ...patch.crop };
  }
  if (patch.transition !== undefined) {
    if (!isTransition(patch.transition)) return null;
    clip.transition = patch.transition;
  }
  if (patch.transitionSec !== undefined) {
    if (!Number.isFinite(patch.transitionSec)) return null;
    clip.transitionSec = round3(Math.min(1.5, Math.max(0.2, patch.transitionSec)));
  }
  return next;
}

export function addClip(project: Project, clip: TimelineClip): Project | null {
  if (project.clips.length >= MAX_CLIPS) return null;
  if (outputSeconds(clip) < MIN_OUTPUT - 1e-6) return null;
  const next = clone(project);
  next.clips.push(clone(clip));
  return next;
}

function squash(raw: string): string {
  let out = '';
  for (const ch of raw) {
    const code = ch.codePointAt(0) ?? 0;
    out += code <= 31 || code === 127 ? ' ' : ch;
  }
  return out.replace(/\s+/g, ' ').trim();
}

export function cleanOverlay(raw: string): string | null {
  const text = squash(raw);
  const count = [...text].length;
  if (count < 1 || count > 80) return null;
  return text;
}

export function addText(project: Project, cue: TextCue): Project | null {
  if (project.texts.length >= MAX_TEXTS) return null;
  const text = cleanOverlay(cue.text);
  if (!text) return null;
  if (
    !Number.isFinite(cue.startSec) ||
    !Number.isFinite(cue.endSec) ||
    cue.endSec - cue.startSec < 0.2 ||
    cue.startSec < 0
  ) {
    return null;
  }
  if (cue.x < 0 || cue.x > 1 || cue.y < 0 || cue.y > 1) return null;
  const next = clone(project);
  next.texts.push({ ...cue, text, startSec: round3(cue.startSec), endSec: round3(cue.endSec) });
  return next;
}

export function patchText(
  project: Project,
  cueId: string,
  patch: Partial<Pick<TextCue, 'text' | 'startSec' | 'endSec'>>,
): Project | null {
  const next = clone(project);
  const cue = next.texts.find((item) => item.id === cueId);
  if (!cue) return null;
  if (patch.text !== undefined) {
    const text = cleanOverlay(patch.text);
    if (!text) return null;
    cue.text = text;
  }
  const start = patch.startSec ?? cue.startSec;
  const end = patch.endSec ?? cue.endSec;
  if (!Number.isFinite(start) || !Number.isFinite(end) || end - start < 0.2 || start < 0) return null;
  cue.startSec = round3(start);
  cue.endSec = round3(end);
  return next;
}

export function removeText(project: Project, cueId: string): Project {
  return { ...clone(project), texts: project.texts.filter((cue) => cue.id !== cueId) };
}

export function setMusic(project: Project, music: MusicTrack): Project | null {
  if (!music.jobId || music.inSec < 0 || music.offsetSec < 0) return null;
  if (!Number.isFinite(music.volume) || !Number.isFinite(music.inSec) || !Number.isFinite(music.offsetSec)) return null;
  return {
    ...clone(project),
    music: {
      ...music,
      volume: round3(clampVolume(music.volume)),
      inSec: round3(music.inSec),
      offsetSec: round3(music.offsetSec),
    },
  };
}

export function clearMusic(project: Project): Project {
  return { ...clone(project), music: null };
}

export function cropFromFrame(zoom: number, panX: number, panY: number): Crop {
  const z = clamp(zoom, 1, 3);
  const w = 1 / z;
  const h = 1 / z;
  const px = clamp(panX, -1, 1);
  const py = clamp(panY, -1, 1);
  return {
    x: round3(((1 - w) * (px + 1)) / 2),
    y: round3(((1 - h) * (py + 1)) / 2),
    w: round3(w),
    h: round3(h),
  };
}

export function frameFromCrop(crop: Crop): { zoom: number; panX: number; panY: number } {
  const zoom = clamp(1 / Math.max(crop.w, 1 / 3), 1, 3);
  const xSpan = 1 - crop.w;
  const ySpan = 1 - crop.h;
  return {
    zoom,
    panX: xSpan < 0.001 ? 0 : clamp((crop.x / xSpan) * 2 - 1, -1, 1),
    panY: ySpan < 0.001 ? 0 : clamp((crop.y / ySpan) * 2 - 1, -1, 1),
  };
}

export function nextRotate(current: Rotate): Rotate {
  switch (current) {
    case 0:
      return 90;
    case 90:
      return 180;
    case 180:
      return 270;
    case 270:
      return 0;
    default: {
      const never: never = current;
      return never;
    }
  }
}

export function exportTitle(raw: string): string {
  const clean = squash(raw);
  const chars = [...clean];
  const cut = (chars.length > 120 ? chars.slice(0, 120).join('') : clean).trim();
  return cut.length > 0 ? cut : 'Ролик';
}

export function toCompose(project: Project, title: string): ComposeRequest {
  const dur = projectDuration(project);
  return {
    title: exportTitle(title),
    clips: project.clips.map((clip, index) => ({
      jobId: clip.jobId,
      inSec: round3(clip.inSec),
      outSec: round3(clip.outSec),
      speed: clip.speed,
      volume: round3(clampVolume(clip.volume)),
      crop: { x: round3(clip.crop.x), y: round3(clip.crop.y), w: round3(clip.crop.w), h: round3(clip.crop.h) },
      rotate: clip.rotate,
      transition: index === 0 ? 'none' : clip.transition,
      transitionSec: index === 0 ? 0 : round3(clip.transitionSec),
    })),
    texts: project.texts.flatMap((cue) => {
      const text = cleanOverlay(cue.text);
      if (!text) return [];
      const start = Math.max(0, cue.startSec);
      const end = Math.min(dur, cue.endSec);
      if (end - start < 0.2) return [];
      return [
        {
          text,
          startSec: round3(start),
          endSec: round3(end),
          x: round3(clamp(cue.x, 0, 1)),
          y: round3(clamp(cue.y, 0, 1)),
        },
      ];
    }),
    music: project.music
      ? {
          jobId: project.music.jobId,
          inSec: round3(Math.max(0, project.music.inSec)),
          volume: round3(clampVolume(project.music.volume)),
          offsetSec: round3(Math.max(0, project.music.offsetSec)),
        }
      : null,
  };
}
