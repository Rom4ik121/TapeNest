import { Download, LayoutDashboard, Moon, Music2, Sun, Users } from 'lucide-react';
import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { NavLink } from 'react-router-dom';
import { ru } from '@/i18n/labels';
import { syncTelegramChrome } from '@/shared/telegram';
import { currentTheme, toggleTheme } from '@/shared/theme';
import type { ThemeName } from '@/shared/theme';

const LINKS = [
  { to: '/', label: ru.navOverview, icon: LayoutDashboard, end: true },
  { to: '/users', label: ru.navUsers, icon: Users, end: false },
  { to: '/downloads', label: ru.navDownloads, icon: Download, end: false },
  { to: '/music', label: ru.navMusic, icon: Music2, end: false },
] as const;

export function Shell({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState<ThemeName>(() => currentTheme());

  useEffect(() => {
    syncTelegramChrome(theme === 'dark' ? '#16141C' : '#E7E4DE');
  }, [theme]);

  return (
    <div className="mx-auto flex min-h-[var(--app-height)] max-w-md flex-col">
      <header className="flex items-center justify-between px-4 pt-[max(0.75rem,var(--app-safe-top))]">
        <p className="text-sm font-semibold tracking-wide text-muted">{ru.brand}</p>
        <button
          type="button"
          className="rounded-full bg-surface p-2 shadow-card"
          aria-label={theme === 'dark' ? ru.themeLight : ru.themeDark}
          onClick={() => setTheme(toggleTheme())}
        >
          {theme === 'dark' ? <Sun size={18} /> : <Moon size={18} />}
        </button>
      </header>
      <main className="flex-1 px-4 py-4">{children}</main>
      <nav className="fixed inset-x-0 bottom-0 z-40 border-t border-line/10 bg-bg/95 backdrop-blur">
        <ul className="mx-auto grid max-w-md grid-cols-4 pb-[var(--app-safe-bottom)]">
          {LINKS.map((link) => (
            <li key={link.to}>
              <NavLink
                to={link.to}
                end={link.end}
                className={({ isActive }) =>
                  `flex flex-col items-center gap-1 px-2 py-2 text-[11px] font-semibold ${isActive ? 'text-magenta' : 'text-muted'}`
                }
              >
                <link.icon size={20} aria-hidden />
                {link.label}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>
    </div>
  );
}
