import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { resolveTheme } from './theme';

describe('resolveTheme', () => {
  it('Telegram colorScheme wins, then prefers-color-scheme, then dark', () => {
    expect(resolveTheme(false, true)).toBe('light');
    expect(resolveTheme(true, false)).toBe('dark');
    expect(resolveTheme(null, false)).toBe('light');
    expect(resolveTheme(null, true)).toBe('dark');
    expect(resolveTheme(null, null)).toBe('dark');
  });
});

// WCAG contrast check straight from theme.css (single source of truth).
const css = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), 'theme.css'), 'utf8');
function tokens(selector: string): Record<string, [number, number, number]> {
  const start = css.indexOf(selector);
  const block = css.slice(css.indexOf('{', start) + 1, css.indexOf('}', start));
  const out: Record<string, [number, number, number]> = {};
  for (const m of block.matchAll(/--c-([\w-]+):\s*(\d+)\s+(\d+)\s+(\d+)/g)) {
    out[m[1]!] = [Number(m[2]), Number(m[3]), Number(m[4])];
  }
  return out;
}
const lum = ([r, g, b]: [number, number, number]): number => {
  const f = (c: number): number => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
};
const contrast = (a: [number, number, number], b: [number, number, number]): number => {
  const [x, y] = [lum(a), lum(b)].sort((m, n) => n - m) as [number, number];
  return (x + 0.05) / (y + 0.05);
};

describe.each([
  ["[data-theme='light']", 'light'],
  ["[data-theme='dark']", 'dark'],
])('%s palette contrast', (selector) => {
  const t = tokens(selector);
  it.each([
    ['fg', 'bg', 7],
    ['fg', 'surface', 7],
    ['muted', 'bg', 4.5],
    ['muted', 'surface', 4.5],
    ['accent-fg', 'accent', 4.5],
    ['accent-ink', 'bg', 4.5],
    ['danger-fg', 'danger', 4.5],
  ] as const)('%s on %s ≥ %s:1', (fg, bg, min) => {
    expect(t[fg], fg).toBeDefined();
    expect(t[bg], bg).toBeDefined();
    expect(contrast(t[fg]!, t[bg]!)).toBeGreaterThanOrEqual(min);
  });
});
