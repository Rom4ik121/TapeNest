import { describe, expect, it } from 'vitest';
import {
  addClip,
  addText,
  clipFromJob,
  commit,
  cropFromFrame,
  emptyHistory,
  frameFromCrop,
  moveClip,
  nextRotate,
  patchClip,
  placeAt,
  projectDuration,
  redo,
  setMusic,
  splitClip,
  toCompose,
  trimClip,
  undo,
  type Project,
  type TimelineClip,
} from './model';

function clip(over: Partial<TimelineClip> = {}): TimelineClip {
  return {
    id: 'a',
    jobId: 'job-a',
    title: 'Клип',
    sourceDuration: 10,
    inSec: 0,
    outSec: 4,
    speed: 1,
    volume: 1,
    crop: { x: 0, y: 0, w: 1, h: 1 },
    rotate: 0,
    transition: 'none',
    transitionSec: 0.5,
    ...over,
  };
}

function project(clips: TimelineClip[]): Project {
  return { clips, texts: [], music: null };
}

describe('timeline model', () => {
  it('splits, trims, reorders and undoes', () => {
    const base = project([clip()]);
    let history = emptyHistory(base);
    const split = splitClip(base, 'a', 2, 'b');
    expect(split?.project.clips.map((item) => [item.inSec, item.outSec])).toEqual([
      [0, 2],
      [2, 4],
    ]);
    history = commit(history, split!.project);
    const trimmed = trimClip(history.present, 'b', 'out', 3.2);
    expect(trimmed.clips[1]?.outSec).toBe(3.2);
    history = commit(history, trimmed);
    const moved = moveClip(history.present, 'b', -1);
    expect(moved.clips.map((item) => item.id)).toEqual(['b', 'a']);
    history = commit(history, moved);
    history = undo(history);
    expect(history.present.clips.map((item) => item.id)).toEqual(['a', 'b']);
    history = undo(history);
    history = undo(history);
    expect(history.present.clips).toHaveLength(1);
    history = redo(history);
    expect(history.present.clips).toHaveLength(2);
    expect(splitClip(base, 'a', 0.1, 'z')).toBeNull();
    expect(commit(history, history.present)).toBe(history);
  });

  it('places the playhead across a fade and rejects a speed that would be too short', () => {
    const left = clip({ id: 'a', outSec: 4 });
    const right = clip({ id: 'b', jobId: 'job-b', inSec: 1, outSec: 5, transition: 'fade', transitionSec: 1 });
    const timeline = project([left, right]);
    expect(projectDuration(timeline)).toBe(7);
    expect(placeAt(timeline, 0)?.clip).toEqual({ index: 0, sourceSec: 0 });
    const mid = placeAt(timeline, 3.5);
    expect(mid?.clip).toEqual({ index: 0, sourceSec: 3.5 });
    expect(mid?.incoming).toEqual({ index: 1, sourceSec: 1.5 });
    expect(mid?.mix).toBe(0.5);
    expect(placeAt(timeline, 4)?.clip).toEqual({ index: 1, sourceSec: 2 });
    expect(placeAt(timeline, 4)?.incoming).toBeNull();

    const fast = patchClip(project([clip({ inSec: 0, outSec: 0.6 })]), 'a', { speed: 2 });
    expect(fast).toBeNull();
    const ok = patchClip(project([clip()]), 'a', { speed: 2, volume: 3, rotate: 90 });
    expect(ok?.clips[0]).toMatchObject({ speed: 2, volume: 1.5, rotate: 90 });
    expect(nextRotate(270)).toBe(0);
  });

  it('exports text, crop, music and a transition into a library compose body', () => {
    const seeded = clipFromJob({ id: 'job-a', title: 'Дом', file: { durationSec: 8 } }, 'a');
    expect(seeded).not.toBeNull();
    let timeline = project([seeded!]);
    const zoomed = patchClip(timeline, 'a', { crop: cropFromFrame(2, 0, 0) });
    expect(zoomed).not.toBeNull();
    timeline = zoomed!;
    expect(frameFromCrop(timeline.clips[0]!.crop).zoom).toBeCloseTo(2, 2);
    const withText = addText(timeline, { id: 't', text: '  Привет  ', startSec: 0.5, endSec: 2, x: 0.5, y: 0.8 });
    expect(withText?.texts[0]?.text).toBe('Привет');
    timeline = withText!;
    const extra = clip({ id: 'b', jobId: 'job-b', transition: 'wipe', transitionSec: 0.4 });
    timeline = addClip(timeline, extra)!;
    timeline = setMusic(timeline, { jobId: 'job-b', title: 'Фон', inSec: 1, volume: 0.4, offsetSec: 0.2 })!;
    const body = toCompose(timeline, '  Мой ролик  ');
    expect(body.title).toBe('Мой ролик');
    expect(body.clips[0]).toMatchObject({ transition: 'none', crop: { x: 0.25, y: 0.25, w: 0.5, h: 0.5 } });
    expect(body.clips[1]).toMatchObject({ transition: 'wipe', transitionSec: 0.4, jobId: 'job-b' });
    expect(body.texts).toEqual([{ text: 'Привет', startSec: 0.5, endSec: 2, x: 0.5, y: 0.8 }]);
    expect(body.music).toEqual({ jobId: 'job-b', inSec: 1, volume: 0.4, offsetSec: 0.2 });
    expect(
      addClip({ ...timeline, clips: Array.from({ length: 8 }, (_, i) => clip({ id: `c${i}` })) }, extra),
    ).toBeNull();
  });
});
