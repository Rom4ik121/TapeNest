import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { VideoJob } from '@/entities/video/types';
import { ApiError } from '@/shared/api/client';
import type * as VideoApi from '@/entities/video/api';
import { VideoLibraryPage } from './LibraryPage';
import { VideoPage } from './VideoPage';

vi.mock('@/entities/video/api', async () => {
  const actual = await vi.importActual<typeof VideoApi>('@/entities/video/api');
  return {
    ...actual,
    downloadsApi: {
      list: vi.fn(),
      get: vi.fn(),
      create: vi.fn(),
      rename: vi.fn(),
      remove: vi.fn(),
      trim: vi.fn(),
      fileUrl: vi.fn(),
    },
  };
});

import { downloadsApi } from '@/entities/video/api';

const api = vi.mocked(downloadsApi);

function job(over: Partial<VideoJob> = {}): VideoJob {
  return {
    id: 'job-1',
    url: 'https://youtu.be/jNQXAC9IVRw',
    source: 'youtube',
    status: 'done',
    title: 'Домашний клип',
    posterUrl: 'https://cdn.example/poster.jpg',
    file: {
      fileName: 'clip.mp4',
      sizeBytes: 1000,
      mimeType: 'video/mp4',
      durationSec: 65,
      width: 640,
      height: 360,
      expiresAt: '2026-10-09T00:00:00Z',
    },
    createdAt: '2026-10-02T12:00:00Z',
    updatedAt: '2026-10-02T12:05:00Z',
    finishedAt: '2026-10-02T12:05:00Z',
    ...over,
  };
}

function renderLibrary() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <VideoLibraryPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function renderPlayer() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/videos/job-1']}>
        <Routes>
          <Route path="/videos/:id" element={<VideoPage />} />
          <Route path="/videos" element={<div>library-root</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('video library', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('lists a download with poster, title, duration and date', async () => {
    api.list.mockResolvedValue({ items: [job()], nextCursor: null });
    renderLibrary();
    expect(await screen.findByText('Домашний клип')).toBeInTheDocument();
    expect(screen.getByText('1:05')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Открыть «Домашний клип»' })).toHaveAttribute('href', '/videos/job-1');
    const img = document.querySelector('img');
    expect(img).toHaveAttribute('src', 'https://cdn.example/poster.jpg');
    expect(screen.getByText(/2026/)).toBeInTheDocument();
  });

  it('shows an empty library and a useful error for a bad link', async () => {
    api.list.mockResolvedValue({ items: [], nextCursor: null });
    api.create.mockRejectedValue(new ApiError(400, 'bad', 'UNSUPPORTED_SOURCE'));
    renderLibrary();
    expect(await screen.findByText(/Пока ничего нет/)).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Ссылка на видео'), { target: { value: 'https://example.com/x' } });
    fireEvent.click(screen.getByRole('button', { name: 'Скачать' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Этот сайт не поддерживается');
    expect(api.create).toHaveBeenCalledWith('https://example.com/x');
  });

  it('renames, trims and deletes the open video', async () => {
    api.get.mockResolvedValue(job());
    api.fileUrl.mockResolvedValue({ url: 'https://cdn.example/clip.mp4' });
    api.rename.mockImplementation(async (_id, title) => job({ title }));
    api.trim.mockResolvedValue(job({ title: 'Фрагмент' }));
    api.remove.mockResolvedValue(undefined);
    renderPlayer();

    expect(await screen.findByRole('heading', { name: 'Домашний клип' })).toBeInTheDocument();
    const video = document.querySelector('video');
    expect(video).toHaveAttribute('src', 'https://cdn.example/clip.mp4');

    fireEvent.change(screen.getByLabelText('Название'), { target: { value: 'Новое имя' } });
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить' }));
    await waitFor(() => expect(api.rename).toHaveBeenCalledWith('job-1', 'Новое имя'));

    fireEvent.change(screen.getByLabelText(/Начало/), { target: { value: '0:02' } });
    fireEvent.change(screen.getByLabelText(/Конец/), { target: { value: '0:08' } });
    fireEvent.click(screen.getByRole('button', { name: 'Сохранить фрагмент' }));
    await waitFor(() => expect(api.trim).toHaveBeenCalledWith('job-1', 2, 8));
    expect(await screen.findByRole('button', { name: 'Удалить' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Удалить' }));
    fireEvent.click(screen.getByRole('button', { name: 'Удалить' }));
    await waitFor(() => expect(api.remove).toHaveBeenCalledWith('job-1'));
    expect(await screen.findByText('library-root')).toBeInTheDocument();
  });
});
