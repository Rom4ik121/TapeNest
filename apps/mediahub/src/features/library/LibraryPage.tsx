import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ImagePlus, Link2 } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';
import { api, apiForm } from '@/shared/api/client';
import { qk } from '@/shared/api/queryClient';
import type { Page } from '@/shared/api/types';

interface DownloadJob {
  id: string;
  url: string;
  status: string;
  title?: string;
  errorMessage?: string;
}

interface PhotoItem {
  id: string;
  title: string;
}

type Segment = 'videos' | 'photos';

export function LibraryPage() {
  const { t } = useTranslation();
  const [segment, setSegment] = useState<Segment>('videos');
  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-2xl font-bold">{t('library.title')}</h1>
      <div role="tablist" className="grid grid-cols-2 gap-2 rounded-full bg-surface-2 p-1">
        {(['videos', 'photos'] as const).map((id) => (
          <button
            key={id}
            type="button"
            role="tab"
            aria-selected={segment === id}
            className={`min-h-11 rounded-full text-sm font-semibold ${segment === id ? 'bg-accent text-accent-fg' : 'text-fg'}`}
            onClick={() => setSegment(id)}
          >
            {t(`library.${id}`)}
          </button>
        ))}
      </div>
      {segment === 'videos' ? <Videos /> : <Photos />}
    </div>
  );
}

function Videos() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [url, setUrl] = useState('');
  const list = useQuery({
    queryKey: qk.downloads,
    queryFn: ({ signal }) => api<Page<DownloadJob>>('/downloads', { query: { limit: 30 }, signal }),
    refetchInterval: (q) => (q.state.data?.items.some((j) => j.status === 'queued' || j.status === 'running') ? 2000 : false),
  });
  const create = useMutation({
    mutationFn: (link: string) => api<DownloadJob>('/downloads', { method: 'POST', body: { url: link } }),
    onSuccess: async () => {
      setUrl('');
      await qc.invalidateQueries({ queryKey: qk.downloads });
    },
  });
  const items = list.data?.items ?? [];
  return (
    <div className="flex flex-col gap-3">
      <form
        className="flex flex-col gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          const link = url.trim();
          if (link) create.mutate(link);
        }}
      >
        <label className="text-sm text-muted" htmlFor="video-url">
          {t('library.paste')}
        </label>
        <div className="flex gap-2">
          <input
            id="video-url"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            inputMode="url"
            autoCapitalize="none"
            autoCorrect="off"
            className="min-h-11 min-w-0 flex-1 rounded-2xl border border-line bg-surface px-3 text-base"
            placeholder="https://"
          />
          <button
            type="submit"
            disabled={create.isPending || url.trim() === ''}
            className="inline-flex min-h-11 items-center gap-2 rounded-full bg-accent px-4 font-semibold text-accent-fg disabled:opacity-50"
          >
            <Link2 className="h-4 w-4" aria-hidden />
            {t('library.download')}
          </button>
        </div>
      </form>
      {list.isError && <p className="text-sm text-danger">{t('common.error')}</p>}
      {items.length === 0 && !list.isLoading && <p className="text-sm text-muted">{t('library.emptyVideos')}</p>}
      <ul className="flex flex-col gap-2">
        {items.map((job) => (
          <li key={job.id} className="flex items-center gap-3 rounded-2xl bg-surface p-3 shadow-card">
            <div className="min-w-0 flex-1">
              <p className="truncate font-semibold">{job.title || job.url}</p>
              <p className="text-xs text-muted">
                {job.status === 'done' ? job.status : job.status === 'failed' ? t('library.failed') : t('library.downloading')}
              </p>
            </div>
            {job.status === 'done' && (
              <button
                type="button"
                className="min-h-11 shrink-0 rounded-full bg-surface-2 px-4 text-sm font-semibold"
                onClick={() => navigate(`/v/${job.id}`)}
              >
                {t('library.edit')}
              </button>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

function Photos() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const list = useQuery({
    queryKey: qk.photos,
    queryFn: ({ signal }) => api<Page<PhotoItem>>('/photos', { signal }),
  });
  const upload = useMutation({
    mutationFn: async (file: File) => {
      const body = new FormData();
      body.append('file', file);
      body.append('title', file.name.replace(/\.[^.]+$/, ''));
      return apiForm<PhotoItem>('/photos', body);
    },
    onSuccess: async (photo) => {
      await qc.invalidateQueries({ queryKey: qk.photos });
      navigate(`/p/${photo.id}`);
    },
  });
  const items = list.data?.items ?? [];
  return (
    <div className="flex flex-col gap-3">
      <label className="inline-flex min-h-11 cursor-pointer items-center justify-center gap-2 rounded-full bg-accent px-4 font-semibold text-accent-fg">
        <ImagePlus className="h-4 w-4" aria-hidden />
        {t('library.addPhoto')}
        <input
          type="file"
          accept="image/jpeg,image/png"
          className="sr-only"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (file) upload.mutate(file);
            e.target.value = '';
          }}
        />
      </label>
      {upload.isError && <p className="text-sm text-danger">{t('common.error')}</p>}
      {items.length === 0 && !list.isLoading && <p className="text-sm text-muted">{t('library.emptyPhotos')}</p>}
      <ul className="grid grid-cols-2 gap-2">
        {items.map((photo) => (
          <li key={photo.id}>
            <button
              type="button"
              className="flex min-h-24 w-full items-end rounded-2xl bg-surface-2 p-3 text-left font-semibold"
              onClick={() => navigate(`/p/${photo.id}`)}
            >
              <span className="line-clamp-2">{photo.title}</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
