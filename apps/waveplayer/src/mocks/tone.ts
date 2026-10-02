/**
 * Procedural music for mock streaming. HTMLAudioElement cannot be intercepted
 * by MSW, so stream-url returns a blob: URL with a generated WAV (a little
 * chord progression with bass + arpeggio, unique per track). Lazily generated;
 * only the last few are kept in memory.
 */
const SAMPLE_RATE = 11025;
const CACHE_LIMIT = 4;
const cache = new Map<string, string>();

function seedOf(id: string): number {
  let s = 0;
  for (const c of id) s = (Math.imul(s, 31) + c.charCodeAt(0)) | 0;
  return Math.abs(s);
}

const PROGRESSIONS = [
  [0, 7, 9, 5], // I V vi IV
  [9, 5, 0, 7], // vi IV I V
  [0, 9, 5, 7], // I vi IV V
  [0, 5, 7, 5],
];

export function renderTrack(id: string, durationSec: number): ArrayBuffer {
  const seed = seedOf(id);
  const total = Math.floor(SAMPLE_RATE * durationSec);
  const buffer = new ArrayBuffer(44 + total * 2);
  const view = new DataView(buffer);
  const w = (o: number, s: string): void => {
    for (let i = 0; i < s.length; i++) view.setUint8(o + i, s.charCodeAt(i));
  };
  w(0, 'RIFF');
  view.setUint32(4, 36 + total * 2, true);
  w(8, 'WAVE');
  w(12, 'fmt ');
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, SAMPLE_RATE, true);
  view.setUint32(28, SAMPLE_RATE * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  w(36, 'data');
  view.setUint32(40, total * 2, true);

  const root = 45 + (seed % 12); // MIDI A2..G#3
  const bpm = 78 + (seed % 40);
  const beat = 60 / bpm;
  const prog = PROGRESSIONS[seed % PROGRESSIONS.length] ?? PROGRESSIONS[0]!;
  const midi = (n: number): number => 440 * Math.pow(2, (n - 69) / 12);
  const TWO_PI = Math.PI * 2;

  for (let i = 0; i < total; i++) {
    const t = i / SAMPLE_RATE;
    const beatPos = t / beat;
    const bar = Math.floor(beatPos / 4);
    const chordRoot = root + (prog[bar % prog.length] ?? 0);
    const minor = (prog[bar % prog.length] ?? 0) === 9;
    const third = minor ? 3 : 4;

    // Arpeggio: 8th notes through the chord, 2 octaves up
    const step = Math.floor(beatPos * 2);
    const arpNotes = [0, third, 7, 12, 7, third];
    const arpNote = chordRoot + 24 + (arpNotes[step % arpNotes.length] ?? 0);
    const stepT = beatPos * 2 - step;
    const arpEnv = Math.exp(-stepT * 3.2);
    const arp = Math.sin(TWO_PI * midi(arpNote) * t) * arpEnv * 0.22;

    // Pad: soft chord
    const pad =
      (Math.sin(TWO_PI * midi(chordRoot + 12) * t) +
        Math.sin(TWO_PI * midi(chordRoot + 12 + third) * t) +
        Math.sin(TWO_PI * midi(chordRoot + 19) * t)) *
      0.06;

    // Bass on every beat
    const bt = beatPos - Math.floor(beatPos);
    const bass = Math.sin(TWO_PI * midi(chordRoot) * t) * Math.exp(-bt * 2.5) * 0.28;

    // Kick: pitch-dropping sine burst
    const kick = Math.sin(TWO_PI * (50 + 90 * Math.exp(-bt * 30)) * bt) * Math.exp(-bt * 12) * 0.35;

    const fade = Math.min(1, t / 1.5) * Math.min(1, (durationSec - t) / 2);
    const s = (arp + pad + bass + kick) * fade * 0.8;
    view.setInt16(44 + i * 2, Math.max(-1, Math.min(1, s)) * 32767, true);
  }
  return buffer;
}

export function toneUrl(id: string, durationSec: number): string {
  const hit = cache.get(id);
  if (hit) return hit;
  const url = URL.createObjectURL(new Blob([renderTrack(id, durationSec)], { type: 'audio/wav' }));
  cache.set(id, url);
  if (cache.size > CACHE_LIMIT) {
    const oldest = cache.keys().next().value;
    if (oldest !== undefined) {
      const u = cache.get(oldest);
      cache.delete(oldest);
      // Revoke later: the element may still be finishing with it.
      if (u) setTimeout(() => URL.revokeObjectURL(u), 60_000);
    }
  }
  return url;
}
