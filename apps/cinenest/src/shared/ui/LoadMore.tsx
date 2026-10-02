import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';

/** IntersectionObserver sentinel that follows nextCursor; a button fallback for no-IO WebViews. */
export function LoadMore({ hasMore, loading, onLoad }: { hasMore: boolean; loading: boolean; onLoad: () => void }) {
  const { t } = useTranslation();
  const ref = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el || !hasMore || typeof IntersectionObserver === 'undefined') return;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting) && !loading) onLoad();
      },
      { rootMargin: '600px 0px' },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [hasMore, loading, onLoad]);
  if (!hasMore) return null;
  return (
    <div ref={ref} className="py-5 text-center text-sm text-muted" aria-live="polite">
      {loading ? (
        t('list.loadingMore')
      ) : (
        <button type="button" onClick={onLoad} className="rounded-full px-4 py-2 font-medium text-accent-ink">
          {t('list.loadingMore')}
        </button>
      )}
    </div>
  );
}
