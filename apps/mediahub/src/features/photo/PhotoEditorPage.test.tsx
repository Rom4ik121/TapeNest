import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import { http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest';
import { PhotoEditorPage } from './PhotoEditorPage';

const server = setupServer(
  http.get('http://api.test/api/v1/photos/photo-1', () =>
    HttpResponse.json({ id: 'photo-1', title: 'Окно', width: 20, height: 16, mimeType: 'image/png', sizeBytes: 10, createdAt: '2026-10-03T00:00:00Z' }),
  ),
  http.get('http://api.test/api/v1/photos/photo-1/file', () => HttpResponse.json({ url: 'https://files.example/p.png' })),
);
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

describe('PhotoEditorPage', () => {
  it('shows crop controls and the color tab', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={['/p/photo-1']}>
          <Routes>
            <Route path="/p/:photoId" element={<PhotoEditorPage />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(await screen.findByRole('heading', { name: 'Окно' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Кадр' })).toHaveAttribute('aria-selected', 'true');
    fireEvent.click(screen.getByRole('tab', { name: 'Цвет' }));
    expect(screen.getByRole('tab', { name: 'Цвет' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('combobox')).toBeInTheDocument();
  });
});
