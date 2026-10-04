import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { downloadLabel, ru } from '@/i18n/labels';
import { api } from '@/shared/api/client';
import type { AdminDownload, DownloadStatus } from '@/shared/api/types';
import { formatWhen, shortUrl } from '@/shared/format';
import { ConfirmDialog } from '@/shared/ui/Confirm';
import { Empty, Failed, Loading } from '@/shared/ui/States';

type Filter = '' | DownloadStatus;

const FILTERS: Filter[] = ['', 'queued', 'running', 'done', 'failed'];

type Pending = { kind: 'retry' | 'remove'; id: string };

const BADGE: Record<DownloadStatus, string> = {
  queued: 'bg-surface-2 text-fg',
  running: 'bg-teal/20 text-teal',
  done: 'bg-amber/25 text-fg',
  failed: 'bg-danger/15 text-danger',
};

export function Downloads() {
  const [status, setStatus] = useState<Filter>('');
  const [pending, setPending] = useState<Pending | null>(null);
  const client = useQueryClient();
  const jobs = useQuery({
    queryKey: ['admin-downloads', status],
    queryFn: () =>
      api<{ items: AdminDownload[]; nextCursor: string | null }>('/admin/downloads', {
        query: { status: status || undefined, limit: 30 },
      }),
  });
  const action = useMutation({
    mutationFn: (next: Pending) => mutateDownload(next),
    onSuccess: async () => {
      setPending(null);
      await client.invalidateQueries({ queryKey: ['admin-downloads'] });
    },
  });

  return (
    <section className="space-y-4">
      <h1 className="text-2xl font-semibold">{ru.downloadsTitle}</h1>
      <p className="text-sm text-muted">{ru.hiddenNote}</p>
      <div className="flex gap-2 overflow-x-auto pb-1">
        {FILTERS.map((filter) => (
          <button
            key={filter || 'all'}
            type="button"
            aria-pressed={status === filter}
            className={`shrink-0 rounded-full px-3 py-1.5 text-sm font-semibold ${
              status === filter ? 'bg-accent text-accent-fg' : 'bg-surface text-fg'
            }`}
            onClick={() => setStatus(filter)}
          >
            {filter === '' ? ru.all : downloadLabel(filter)}
          </button>
        ))}
      </div>
      {jobs.isPending ? <Loading /> : null}
      {jobs.isError ? <Failed onRetry={() => void jobs.refetch()} /> : null}
      {action.isError ? <p className="text-sm text-danger">{ru.error}</p> : null}
      {jobs.data && jobs.data.items.length === 0 ? <Empty /> : null}
      {jobs.data && jobs.data.items.length > 0 ? (
        <ul className="space-y-2">
          {jobs.data.items.map((job) => (
            <li key={job.id} className="rounded-2xl bg-surface px-4 py-3 shadow-card">
              <div className="flex items-start justify-between gap-3">
                <p className="font-medium">{job.title || shortUrl(job.url)}</p>
                <span className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-semibold ${BADGE[job.status]}`}>
                  {downloadLabel(job.status)}
                </span>
              </div>
              <p className="mt-1 break-all text-xs text-muted">{shortUrl(job.url)}</p>
              <p className="mt-1 text-xs text-muted">
                {job.source} · {formatWhen(job.createdAt)} · {job.userId.slice(0, 8)}
              </p>
              {job.errorMessage ? <p className="mt-2 text-sm text-danger">{job.errorMessage}</p> : null}
              {job.status === 'failed' ? (
                <button
                  type="button"
                  className="mt-3 rounded-full bg-accent px-4 py-2 text-sm font-semibold text-accent-fg"
                  onClick={() => setPending({ kind: 'retry', id: job.id })}
                >
                  {ru.retryJob}
                </button>
              ) : null}
              {job.status === 'done' ? (
                <button
                  type="button"
                  className="mt-3 rounded-full bg-danger px-4 py-2 text-sm font-semibold text-danger-fg"
                  onClick={() => setPending({ kind: 'remove', id: job.id })}
                >
                  {ru.removeJob}
                </button>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
      {pending ? (
        <ConfirmDialog
          title={pending.kind === 'remove' ? ru.confirmRemoveTitle : ru.confirmRetryTitle}
          body={pending.kind === 'remove' ? ru.confirmRemoveBody : ru.confirmRetryBody}
          danger={pending.kind === 'remove'}
          busy={action.isPending}
          onCancel={() => {
            if (!action.isPending) setPending(null);
          }}
          onConfirm={() => action.mutate(pending)}
        />
      ) : null}
    </section>
  );
}

async function mutateDownload(next: Pending): Promise<void> {
  switch (next.kind) {
    case 'retry':
      await api(`/admin/downloads/${next.id}/retry`, { method: 'POST' });
      return;
    case 'remove':
      await api(`/admin/downloads/${next.id}`, { method: 'DELETE' });
      return;
    default: {
      const unexpected: never = next.kind;
      throw new Error(unexpected);
    }
  }
}
