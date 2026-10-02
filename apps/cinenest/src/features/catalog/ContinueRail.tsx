import { Play } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { progressPct } from '@/entities/title/files';
import { useContinueWatching } from '@/entities/title/queries';
import { formatTime } from '@/shared/lib/format';
import { haptic } from '@/shared/telegram';
import { Poster } from '@/shared/ui/Poster';
import { SectionHeader } from '@/shared/ui/SectionHeader';

/** "Continue watching": opens the player directly at the saved position. Hidden when empty/failed. */
export function ContinueRail() {
  const { t } = useTranslation();
  const q = useContinueWatching();
  if (!q.isSuccess || q.data.length === 0) return null;
  return (
    <section aria-labelledby="continue-h">
      <SectionHeader title={t('catalog.continue')} id="continue-h" />
      <div className="no-scrollbar -mx-4 flex gap-3 overflow-x-auto px-4 pb-1">
        {q.data.map(({ title, file, position }) => (
          <Link
            key={`${title.id}:${file.id}`}
            to={`/watch/${encodeURIComponent(title.id)}/${encodeURIComponent(file.id)}`}
            onClick={() => haptic('light')}
            className="w-60 shrink-0 rounded-2xl transition active:scale-[0.98]"
            aria-label={`${title.title} — ${t('title.continueFrom', { time: formatTime(position.positionSec) })}`}
          >
            <div className="relative overflow-hidden rounded-2xl shadow-card">
              <Poster id={title.id} src={title.posterUrl} alt="" aspect="wide" className="rounded-2xl" />
              <div className="absolute inset-0 bg-gradient-to-t from-ink/80 via-ink/10 to-transparent" />
              <span className="absolute left-3 top-3 grid h-9 w-9 place-items-center rounded-full bg-cream/90 text-ink">
                <Play className="h-4 w-4 translate-x-[1px] fill-current" aria-hidden />
              </span>
              <div className="absolute inset-x-3 bottom-2.5 text-cream">
                <p className="truncate text-sm font-bold">{title.title}</p>
                <p className="text-[11px] text-cream/80">
                  {file.season !== null && file.episode !== null
                    ? `${t('catalog.episodeShort', { s: file.season, e: file.episode })} · `
                    : ''}
                  {t('catalog.left', { time: formatTime(Math.max(0, position.durationSec - position.positionSec)) })}
                </p>
                <div className="mt-1.5 h-1 overflow-hidden rounded-full bg-cream/25">
                  <div className="h-full rounded-full bg-amber" style={{ width: `${progressPct(position)}%` }} />
                </div>
              </div>
            </div>
          </Link>
        ))}
      </div>
    </section>
  );
}
