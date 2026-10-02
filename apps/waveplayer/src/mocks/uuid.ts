/** Deterministic UUID-v4-shaped id from a seed (mock backend only). */
function fnv1a(input: string, seed: number): number {
  let h = 0x811c9dc5 ^ seed;
  for (let i = 0; i < input.length; i++) {
    h ^= input.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

export function uuidFromSeed(seed: string): string {
  const hex = [0, 1, 2, 3]
    .map((i) =>
      fnv1a(seed, i * 0x9e3779b1)
        .toString(16)
        .padStart(8, '0'),
    )
    .join('');
  const chars = hex.split('');
  chars[12] = '4'; // version 4
  chars[16] = ((parseInt(chars[16] ?? '0', 16) & 0x3) | 0x8).toString(16); // RFC 4122 variant
  const h = chars.join('');
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20, 32)}`;
}

export const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
