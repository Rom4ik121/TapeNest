import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { MemoryRouter } from 'react-router-dom';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { LibraryPage } from './LibraryPage';

const server = setupServer(
  http.get('http://api.test/api/v1/downloads', () =>
    HttpResponse.json({
      items: [{ id: 'job-1', url: 'https://youtu.be/abcdefghijk', status: 'done', title: 'Кухня' }],
      nextCursor: null,
    }),
  ),
  http.get('http://api.test/api/v1/photos', () =>
    HttpResponse.json({ items: [{ id: 'photo-1', title: 'Окно' }], nextCursor: null }),
  ),
);
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderLibrary() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <LibraryPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('LibraryPage', () => {
  it('lists a finished download and switches to photos', async () => {
    renderLibrary();
    expect(await screen.findByText('Кухня')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Монтаж' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: 'Фото' }));
    expect(await screen.findByText('Окно')).toBeInTheDocument();
  });

  it('posts a pasted link', async () => {
    let posted = '';
    server.use(
      http.post('http://api.test/api/v1/downloads', async ({ request }) => {
        posted = await request.text();
        return HttpResponse.json({ id: 'job-2', url: 'https://youtu.be/zzzzzzzzzzz', status: 'queued' });
      }),
    );
    renderLibrary();
    await screen.findByText('Кухня');
    fireEvent.change(screen.getByLabelText('Ссылка на видео'), { target: { value: 'https://youtu.be/zzzzzzzzzzz' } });
    fireEvent.click(screen.getByRole('button', { name: 'Скачать' }));
    await waitFor(() => expect(posted).toContain('zzzzzzzzzzz'));
  });
});
