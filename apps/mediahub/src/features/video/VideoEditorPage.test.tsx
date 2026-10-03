import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { VideoEditorPage } from './VideoEditorPage';

const project = {
  id: 'p1',
  sourceJobId: 'job-1',
  title: 'Ролик',
  durationSec: 8,
  width: 320,
  height: 180,
  hasMusic: false,
  recipe: {
    clips: [
      {
        startSec: 0,
        endSec: 8,
        speed: 1,
        volume: 1,
        crop: { x: 0, y: 0, w: 1, h: 1 },
        rotate: 0,
        transition: 'none',
        transitionSec: 0.4,
      },
    ],
    texts: [],
    musicVolume: 0.35,
    muteSource: false,
  },
};

const server = setupServer(
  http.post('http://api.test/api/v1/video/projects', () => HttpResponse.json(project)),
  http.put('http://api.test/api/v1/video/projects/p1', async ({ request }) => {
    const body = (await request.json()) as { recipe: typeof project.recipe };
    return HttpResponse.json({ ...project, recipe: body.recipe });
  }),
);
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

describe('VideoEditorPage', () => {
  it('opens on the clip tab and moves to export', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={['/v/job-1']}>
          <Routes>
            <Route path="/v/:jobId" element={<VideoEditorPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(await screen.findByRole('heading', { name: 'Ролик' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Клип' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('button', { name: 'Разделить' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: 'Экспорт' }));
    expect(screen.getByRole('tab', { name: 'Экспорт' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('button', { name: 'Собрать ролик' })).toBeInTheDocument();
  });
});
