export type ThemeName = 'light' | 'dark';

export function currentTheme(): ThemeName {
  return document.documentElement.getAttribute('data-theme') === 'light' ? 'light' : 'dark';
}

export function applyTheme(theme: ThemeName): void {
  document.documentElement.setAttribute('data-theme', theme);
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.setAttribute('content', theme === 'dark' ? '#16141C' : '#E7E4DE');
}

export function toggleTheme(): ThemeName {
  const next: ThemeName = currentTheme() === 'dark' ? 'light' : 'dark';
  applyTheme(next);
  return next;
}
