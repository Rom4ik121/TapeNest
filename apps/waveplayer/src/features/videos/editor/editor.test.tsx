import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ComposeRequest, VideoJob } from '@/entities/video/types';
import type * as VideoApi from '@/entities/video/api';
import { EditorPage } from './EditorPage';

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
      compose: vi.fn(),
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
      durationSec: 10,
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

function renderEditor() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/videos/job-1/edit']}>
        <Routes>
          <Route path="/videos/:id/edit" element={<EditorPage />} />
          <Route path="/videos/:id" element={<div>saved-video</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('timeline editor', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.get.mockResolvedValue(job());
    api.list.mockResolvedValue({
      items: [job(), job({ id: 'job-2', title: 'Второй ролик', posterUrl: undefined })],
      nextCursor: null,
    });
    api.fileUrl.mockImplementation(async (id) => ({ url: `https://cdn.example/${id}.mp4` }));
    api.compose.mockResolvedValue(job({ id: 'job-9', title: 'Сборка' }));
  });

  it('splits, undoes, styles and exports a timeline', async () => {
    renderEditor();
    const preview = await screen.findByLabelText('Предпросмотр');
    await waitFor(() => expect(preview.querySelector('video')).toHaveAttribute('src', 'https://cdn.example/job-1.mp4'));
    expect(screen.getAllByRole('listitem')).toHaveLength(1);

    fireEvent.change(screen.getByLabelText('Позиция'), { target: { value: '4' } });
    fireEvent.click(screen.getByRole('button', { name: 'Разделить' }));
    expect(screen.getAllByRole('listitem')).toHaveLength(2);

    fireEvent.click(screen.getByRole('button', { name: 'Отменить' }));
    expect(screen.getAllByRole('listitem')).toHaveLength(1);

    fireEvent.click(screen.getByRole('button', { name: 'Скорость 2×' }));
    fireEvent.change(screen.getByLabelText('Позиция'), { target: { value: '1' } });
    fireEvent.click(screen.getByRole('button', { name: 'Надпись' }));
    expect(within(preview).getByText('Текст')).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText('Позиция'), { target: { value: '4' } });
    expect(within(preview).queryByText('Текст')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Ещё клип' }));
    fireEvent.click(screen.getByRole('button', { name: 'Добавить «Второй ролик»' }));
    fireEvent.click(screen.getByRole('button', { name: /Клип 2/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Затемнение' }));
    fireEvent.click(screen.getByRole('button', { name: /Повернуть/ }));

    fireEvent.click(screen.getByRole('button', { name: 'Дорожка' }));
    fireEvent.click(screen.getByRole('button', { name: 'Звук из «Второй ролик»' }));
    expect(screen.getByText('Музыка: Второй ролик')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Экспортировать' }));
    await waitFor(() => expect(api.compose).toHaveBeenCalledTimes(1));
    const body = api.compose.mock.calls[0]?.[0] as ComposeRequest;
    expect(body.title).toBe('Домашний клип · монтаж');
    expect(body.clips).toHaveLength(2);
    expect(body.clips[0]).toMatchObject({ jobId: 'job-1', speed: 2, transition: 'none' });
    expect(body.clips[1]).toMatchObject({ jobId: 'job-2', transition: 'fade', rotate: 90 });
    expect(body.texts[0]?.text).toBe('Текст');
    expect(body.music).toMatchObject({ jobId: 'job-2', volume: 0.6 });
    expect(await screen.findByText('saved-video')).toBeInTheDocument();
  });
});
