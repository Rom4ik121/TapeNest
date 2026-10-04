import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import type { ReactNode } from 'react';
import { HashRouter, MemoryRouter, Navigate, Route, Routes } from 'react-router-dom';
import { ru } from '@/i18n/labels';
import { Downloads } from '@/screens/Downloads';
import { Music } from '@/screens/Music';
import { Overview } from '@/screens/Overview';
import { UserDetail, Users } from '@/screens/Users';
import { initTelegram } from '@/shared/telegram';
import { Failed, Loading } from '@/shared/ui/States';
import { Shell } from '@/app/Shell';
import { openAdmin } from '@/app/session';
import type { GateState } from '@/app/session';

export function Gate({ children }: { children: ReactNode }) {
  const session = useQuery({
    queryKey: ['admin-session'],
    queryFn: () => openAdmin(initTelegram()),
  });
  if (session.isPending) return <Loading />;
  if (session.isError) return <Failed text={ru.loginFailed} onRetry={() => void session.refetch()} />;
  return <Gated state={session.data}>{children}</Gated>;
}

function Gated({ state, children }: { state: GateState; children: ReactNode }) {
  switch (state.kind) {
    case 'outside':
      return <Notice title={ru.openInTelegram} />;
    case 'refused':
      return <Notice title={ru.refusedTitle} body={ru.refusedBody} />;
    case 'ready':
      return children;
    default: {
      const unexpected: never = state;
      return unexpected;
    }
  }
}

function Notice({ title, body }: { title: string; body?: string }) {
  return (
    <div className="mx-auto flex min-h-[var(--app-height)] max-w-md flex-col justify-center px-6 text-center">
      <p className="text-xs font-semibold uppercase tracking-[0.18em] text-muted">{ru.brand}</p>
      <h1 className="mt-3 text-2xl font-semibold">{title}</h1>
      {body ? <p className="mt-3 text-sm text-muted">{body}</p> : null}
    </div>
  );
}

export function AdminRoutes() {
  return (
    <Shell>
      <Routes>
        <Route path="/" element={<Overview />} />
        <Route path="/users" element={<Users />} />
        <Route path="/users/:id" element={<UserDetail />} />
        <Route path="/downloads" element={<Downloads />} />
        <Route path="/music" element={<Music />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Shell>
  );
}

export function App({ routing = 'hash', initialPath = '/' }: { routing?: 'hash' | 'memory'; initialPath?: string }) {
  const [client] = useState(
    () => new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } }),
  );
  const tree = (
    <Gate>
      <AdminRoutes />
    </Gate>
  );
  const future = { v7_startTransition: true, v7_relativeSplatPath: true } as const;
  const routed =
    routing === 'memory' ? (
      <MemoryRouter initialEntries={[initialPath]} future={future}>
        {tree}
      </MemoryRouter>
    ) : (
      <HashRouter future={future}>{tree}</HashRouter>
    );
  return <QueryClientProvider client={client}>{routed}</QueryClientProvider>;
}
