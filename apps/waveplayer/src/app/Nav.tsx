import { Home, Library, Search, Waves } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { NavLink } from 'react-router-dom';
import { haptic } from '@/shared/telegram';
import { cn } from '@/shared/lib/cn';

const ITEMS = [
  { to: '/', icon: Home, key: 'nav.home', end: true },
  { to: '/wave', icon: Waves, key: 'nav.wave', end: false },
  { to: '/search', icon: Search, key: 'nav.search', end: false },
  { to: '/library', icon: Library, key: 'nav.library', end: false },
] as const;

export function Nav() {
  const { t } = useTranslation();
  return (
    <nav className="bg-glass border-t border-line pb-safe" aria-label="main">
      <ul className="mx-auto flex max-w-lg">
        {ITEMS.map(({ to, icon: Icon, key, end }) => (
          <li key={to} className="flex-1">
            <NavLink
              to={to}
              end={end}
              onClick={() => haptic('selection')}
              className={({ isActive }) =>
                cn(
                  'flex flex-col items-center gap-1 pb-1 pt-2 text-[11px] font-semibold transition-colors',
                  isActive ? 'text-accent-ink' : 'text-muted',
                )
              }
            >
              {({ isActive }) => (
                <>
                  <span
                    className={cn(
                      'grid h-8 w-12 place-items-center rounded-full transition-colors',
                      isActive && 'bg-accent/15',
                    )}
                  >
                    <Icon className="h-[22px] w-[22px]" strokeWidth={isActive ? 2.4 : 2} />
                  </span>
                  {t(key)}
                </>
              )}
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  );
}
