import { useQuery } from '@tanstack/react-query';
import { ru, healthLabel, serviceName } from '@/i18n/labels';
import { api } from '@/shared/api/client';
import type { HealthService, HealthStatus } from '@/shared/api/types';
import { Failed, Loading } from '@/shared/ui/States';

const DOT: Record<HealthStatus, string> = {
  ok: 'bg-teal',
  down: 'bg-danger',
  off: 'bg-muted',
};

export function Overview() {
  const health = useQuery({
    queryKey: ['admin-health'],
    queryFn: () => api<{ services: HealthService[] }>('/admin/health'),
  });

  return (
    <section className="space-y-4">
      <header className="overflow-hidden rounded-3xl p-5 text-cream shadow-card" style={{ background: 'var(--hero)' }}>
        <p className="text-xs font-semibold uppercase tracking-[0.18em] text-cream/80">{ru.brand}</p>
        <h1 className="mt-1 text-2xl font-semibold">{ru.title}</h1>
        <p className="mt-2 text-sm text-cream/85">{ru.healthTitle}</p>
      </header>
      {health.isPending ? <Loading /> : null}
      {health.isError ? <Failed onRetry={() => void health.refetch()} /> : null}
      {health.data ? (
        <ul className="space-y-2">
          {health.data.services.map((service) => (
            <li
              key={service.name}
              className="flex items-center justify-between rounded-2xl bg-surface px-4 py-3 shadow-card"
            >
              <span className="font-medium">{serviceName(service.name)}</span>
              <span className="flex items-center gap-2 text-sm text-muted">
                <span className={`h-2.5 w-2.5 rounded-full ${DOT[service.status]}`} aria-hidden />
                {healthLabel(service.status)}
              </span>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}
