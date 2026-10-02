import { Check } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { groupBySeason, sortByQuality } from '@/entities/title/files';
import type { MediaFile, Title } from '@/shared/api/types';
import { cn } from '@/shared/lib/cn';
import { formatBytes } from '@/shared/lib/format';
import { haptic } from '@/shared/telegram';

interface Props {
  title: Title;
  selectedId: string | null;
  onSelect: (f: MediaFile) => void;
}

/** Torrent files: versions (quality/size) for movies, season → episode → quality for series. */
export function FilePicker({ title, selectedId, onSelect }: Props) {
  const { t, i18n } = useTranslation();
  const selected = title.files.find((f) => f.id === selectedId) ?? null;
  const seasons = groupBySeason(title.files);
  const [season, setSeason] = useState<number>(selected?.season ?? seasons[0]?.season ?? 0);
  const pick = (f: MediaFile): void => {
    haptic('selection');
    onSelect(f);
  };
  const meta = (f: MediaFile) =>
    t('title.fileMeta', { quality: f.quality, size: formatBytes(f.sizeBytes, i18n.language) });

  if (title.kind === 'movie') {
    return (
      <section aria-labelledby="versions-h" className="mt-6">
        <h2 id="versions-h" className="mb-2 px-1 text-lg font-bold">
          {t('title.versions')}
        </h2>
        <ul className="space-y-2" role="radiogroup" aria-labelledby="versions-h">
          {sortByQuality(title.files).map((f) => {
            const on = f.id === selectedId;
            return (
              <li key={f.id}>
                <button
                  type="button"
                  role="radio"
                  aria-checked={on}
                  onClick={() => pick(f)}
                  className={cn(
                    'flex w-full items-center gap-3 rounded-2xl px-4 py-3 text-left transition active:scale-[0.99]',
                    on ? 'bg-accent/15 ring-2 ring-accent' : 'bg-surface shadow-card',
                  )}
                >
                  <span className="rounded-lg bg-fg/10 px-2 py-1 text-xs font-extrabold tabular-nums">{f.quality}</span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-semibold">{f.name}</span>
                    <span className="text-xs text-muted">{formatBytes(f.sizeBytes, i18n.language)}</span>
                  </span>
                  {on && <Check className="h-5 w-5 text-accent-ink" aria-hidden />}
                </button>
              </li>
            );
          })}
        </ul>
      </section>
    );
  }

  const current = seasons.find((s) => s.season === season) ?? seasons[0];
  const episodes = new Map<number, MediaFile[]>();
  for (const f of current?.files ?? []) {
    const list = episodes.get(f.episode ?? 0) ?? [];
    list.push(f);
    episodes.set(f.episode ?? 0, list);
  }

  return (
    <section aria-labelledby="episodes-h" className="mt-6">
      <h2 id="episodes-h" className="mb-2 px-1 text-lg font-bold">
        {t('title.episodes')}
      </h2>
      {seasons.length > 1 && (
        <div className="no-scrollbar mb-3 flex gap-2 overflow-x-auto" role="tablist">
          {seasons.map((s) => (
            <button
              key={s.season}
              type="button"
              role="tab"
              aria-selected={s.season === current?.season}
              onClick={() => {
                haptic('selection');
                setSeason(s.season);
              }}
              className={cn(
                'min-h-[36px] shrink-0 rounded-full px-4 text-sm font-semibold transition',
                s.season === current?.season ? 'bg-fg text-bg' : 'bg-surface shadow-card',
              )}
            >
              {t('title.season', { n: s.season })}
            </button>
          ))}
        </div>
      )}
      <ul className="divide-y divide-fg/[0.08] overflow-hidden rounded-2xl bg-surface shadow-card">
        {[...episodes.entries()].map(([ep, files]) => (
          <li key={ep} className="flex items-center gap-3 px-4 py-3">
            <span className="min-w-0 flex-1 text-sm font-semibold">{t('title.episode', { n: ep })}</span>
            <div className="flex gap-1.5" role="radiogroup" aria-label={t('title.episode', { n: ep })}>
              {sortByQuality(files).map((f) => {
                const on = f.id === selectedId;
                return (
                  <button
                    key={f.id}
                    type="button"
                    role="radio"
                    aria-checked={on}
                    aria-label={meta(f)}
                    onClick={() => pick(f)}
                    className={cn(
                      'min-h-[32px] rounded-full px-2.5 text-xs font-bold tabular-nums transition active:scale-95',
                      on ? 'bg-accent text-accent-fg' : 'bg-fg/[0.07] text-fg',
                    )}
                  >
                    {f.quality}
                  </button>
                );
              })}
            </div>
          </li>
        ))}
      </ul>
      {selected && (
        <p className="mt-2 px-1 text-xs text-muted">{`${t('title.selected')}: ${selected.name} · ${formatBytes(selected.sizeBytes, i18n.language)}`}</p>
      )}
    </section>
  );
}
