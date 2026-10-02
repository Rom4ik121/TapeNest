import { Heart, Library, Music2, Play, Waves } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate } from 'react-router-dom';
import { useAuthStore } from '@/entities/auth/authStore';
import { playerStore, usePlayer } from '@/entities/player';
import { useTrackList } from '@/entities/track/queries';
import { TrackList } from '@/entities/track/TrackList';
import { config } from '@/shared/config';
import { dayPart } from '@/shared/lib/format';
import { Cover } from '@/shared/ui/Cover';
import { SectionHeader } from '@/shared/ui/SectionHeader';
import { Skeleton, TrackListSkeleton } from '@/shared/ui/Skeleton';
import { EmptyState, ErrorState } from '@/shared/ui/States';
import { haptic } from '@/shared/telegram';

export function HomePage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const name = useAuthStore((s) => s.user?.firstName ?? '');
  const recent = useTrackList('recent');
  const popular = useTrackList('popular');
  const waveActive = usePlayer((s) => s.waveSessionId !== null);

  const startWave = (): void => {
    haptic('medium');
    if (!waveActive) void playerStore.getState().startWave();
    navigate('/wave');
  };

  return (
    <div>
      <header className="px-1 pt-2">
        <p className="text-sm font-medium text-muted">{t('home.subtitle')}</p>
        <h1 className="mt-0.5 text-[28px] font-extrabold leading-tight tracking-tight">
          {t(`home.greeting.${dayPart(new Date().getHours())}`, { name })}
        </h1>
        {config.isMocked('music') && (
          <span className="mt-2 inline-flex items-center gap-1.5 rounded-full bg-teal/15 px-2.5 py-1 text-[11px] font-semibold text-fg">
            <span className="h-1.5 w-1.5 rounded-full bg-teal" aria-hidden /> {t('home.demo')}
          </span>
        )}
      </header>

      {/* Wave hero — gradient 04 */}
      <button
        type="button"
        onClick={startWave}
        className="scrim on-gradient relative mt-5 block w-full overflow-hidden rounded-4xl bg-grad-04 p-5 text-left text-cream shadow-glow active:scale-[0.99]"
      >
        <div className="absolute -right-10 -top-10 h-44 w-44 animate-float rounded-full bg-amber/40 blur-2xl" />
        <div className="absolute -bottom-16 left-10 h-40 w-40 animate-float rounded-full bg-teal/25 blur-2xl [animation-delay:-3s]" />
        <svg
          className="absolute inset-x-0 bottom-0 h-12 w-full opacity-30"
          viewBox="0 0 400 80"
          preserveAspectRatio="none"
          aria-hidden
        >
          <path d="M0 50 Q50 20 100 50 T200 50 T300 50 T400 50 V80 H0Z" fill="#16141C" />
          <path
            d="M0 60 Q50 35 100 60 T200 60 T300 60 T400 60"
            stroke="#E7E4DE"
            strokeOpacity=".5"
            strokeWidth="2"
            fill="none"
          />
        </svg>
        <div className="relative flex items-center justify-between">
          <div>
            <p className="flex items-center gap-2 text-xs font-bold uppercase tracking-[0.2em] text-cream/85">
              <Waves className="h-4 w-4" aria-hidden /> TapeNest
            </p>
            <h2 className="mt-2 text-3xl font-extrabold tracking-tight">{t('home.waveTitle')}</h2>
            <p className="mt-1 max-w-[15rem] text-sm text-cream/85">{t('home.waveSubtitle')}</p>
          </div>
          <span className="grid h-16 w-16 shrink-0 place-items-center rounded-full bg-cream text-ink shadow-lg">
            <Play className="h-7 w-7 translate-x-[2px] fill-current" aria-hidden />
          </span>
        </div>
      </button>

      {/* Quick tiles */}
      <div className="mt-3 grid grid-cols-2 gap-3">
        <Link
          to="/liked"
          onClick={() => haptic('selection')}
          className="relative flex items-center gap-3 overflow-hidden rounded-3xl bg-grad-05 p-3 font-bold text-cream shadow-card transition active:scale-[0.98]"
        >
          <span className="relative grid h-10 w-10 place-items-center rounded-2xl bg-cream/15 ring-1 ring-cream/25">
            <Heart className="h-5 w-5 fill-current" aria-hidden />
          </span>
          <span className="relative">{t('home.liked')}</span>
        </Link>
        <Link
          to="/library"
          onClick={() => haptic('selection')}
          className="flex items-center gap-3 rounded-3xl bg-surface p-3 font-bold shadow-card transition active:scale-[0.98]"
        >
          <span className="grid h-10 w-10 place-items-center rounded-2xl bg-grad-02 text-cream">
            <Library className="h-5 w-5" aria-hidden />
          </span>
          {t('nav.library')}
        </Link>
      </div>

      <SectionHeader title={t('home.recent')} />
      <div className="no-scrollbar -mx-4 flex gap-3 overflow-x-auto px-4 pb-1">
        {recent.isPending &&
          Array.from({ length: 4 }, (_, i) => (
            <div key={i} className="w-32 shrink-0">
              <Skeleton className="h-32 w-32 rounded-2xl" />
              <Skeleton className="mt-2 h-3 w-24" />
            </div>
          ))}
        {recent.isError && (
          <div className="w-full px-0">
            <ErrorState error={recent.error} onRetry={() => void recent.refetch()} retrying={recent.isFetching} />
          </div>
        )}
        {recent.isSuccess && recent.tracks.length === 0 && (
          <div className="w-full">
            <EmptyState icon={<Music2 className="h-8 w-8" aria-hidden />}>{t('list.empty')}</EmptyState>
          </div>
        )}
        {recent.tracks.map((tr) => (
          <button
            key={tr.id}
            type="button"
            onClick={() => {
              haptic('light');
              playerStore.getState().playTrack(tr, recent.tracks);
            }}
            aria-label={`${tr.title} — ${tr.artist}`}
            className="w-32 shrink-0 rounded-2xl text-left transition active:scale-[0.97]"
          >
            <Cover id={tr.id} src={tr.coverUrl} alt="" className="h-32 w-32 shadow-card" rounded="rounded-2xl" />
            <p className="mt-2 truncate text-sm font-semibold">{tr.title}</p>
            <p className="truncate text-xs text-muted">{tr.artist}</p>
          </button>
        ))}
      </div>

      <SectionHeader title={t('home.popular')} />
      {popular.isPending && <TrackListSkeleton />}
      {popular.isError && (
        <ErrorState error={popular.error} onRetry={() => void popular.refetch()} retrying={popular.isFetching} />
      )}
      {popular.isSuccess && (
        <TrackList
          tracks={popular.tracks}
          hasMore={popular.hasNextPage}
          loadingMore={popular.isFetchingNextPage}
          onLoadMore={() => void popular.fetchNextPage()}
        />
      )}
    </div>
  );
}
