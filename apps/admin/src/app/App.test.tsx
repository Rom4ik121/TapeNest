import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { App } from '@/app/App';
import { ru } from '@/i18n/labels';
import { setAccessToken } from '@/shared/api/client';

vi.mock('@/shared/telegram', () => ({
  initTelegram: () => ({
    inTelegram: true,
    initDataRaw: 'user=%7B%22id%22%3A7%7D',
    user: { id: 7, firstName: 'Анна', languageCode: 'ru' },
    isDark: true,
  }),
  syncTelegramChrome: () => undefined,
  haptic: () => undefined,
}));

const calls: string[] = [];
let refused = false;

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const users = {
  items: [
    {
      id: '11111111-1111-1111-1111-111111111111',
      telegramId: 7,
      firstName: 'Анна',
      lastName: 'Иванова',
      username: 'anna',
      languageCode: 'ru',
      role: 'user',
      createdAt: '2026-01-02T00:00:00Z',
      lastLoginAt: '2026-04-01T10:00:00Z',
    },
  ],
};

const music = {
  userId: users.items[0]?.id,
  likes: [{ id: 't1', title: 'Лунная', artist: 'Поле', album: 'Ночь', at: '2026-03-01T00:00:00Z' }],
  recent: [],
  artists: [{ name: 'Поле', count: 4 }],
  genres: [{ name: 'инди', count: 2 }],
};

const downloads = {
  items: [
    {
      id: 'job-done',
      userId: '22222222-2222-2222-2222-222222222222',
      url: 'https://example.com/watch/hidden',
      source: 'youtube',
      status: 'done',
      title: 'Скрытый файл',
      attempts: 1,
      createdAt: '2026-04-01T00:00:00Z',
    },
    {
      id: 'job-failed',
      userId: '22222222-2222-2222-2222-222222222222',
      url: 'https://example.com/watch/fail',
      source: 'youtube',
      status: 'failed',
      title: 'Сбой',
      attempts: 4,
      errorMessage: 'сеть',
      createdAt: '2026-04-02T00:00:00Z',
    },
  ],
  nextCursor: null,
};

beforeEach(() => {
  calls.length = 0;
  refused = false;
  setAccessToken(null);
  vi.stubGlobal('fetch', async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const method = init?.method ?? 'GET';
    calls.push(`${method} ${url}`);
    if (url.endsWith('/auth/telegram')) return json({ accessToken: 'admin-token' });
    if (url.endsWith('/admin/session')) {
      return refused
        ? json({ code: 'FORBIDDEN', message: 'not on the admin allowlist' }, 403)
        : json({ telegramId: 7, role: 'admin' });
    }
    if (url.endsWith('/admin/health')) {
      return json({
        services: [
          { name: 'gateway', status: 'ok' },
          { name: 'bot', status: 'ok' },
          { name: 'music', status: 'down' },
          { name: 'downloads', status: 'ok' },
          { name: 'recommendations', status: 'off' },
        ],
      });
    }
    if (url.endsWith('/admin/users')) return json(users);
    if (url.includes('/admin/users/') && url.endsWith('/music')) return json(music);
    if (url.includes('/admin/downloads') && method === 'GET') return json(downloads);
    if (url.endsWith('/retry') && method === 'POST') {
      return json({ ...downloads.items[1], status: 'queued', attempts: 0 });
    }
    if (method === 'DELETE') return new Response(null, { status: 204 });
    if (url.endsWith('/admin/catalog')) {
      return json({
        artists: 3,
        albums: 5,
        tracks: 12,
        deletedTracks: 1,
        youtubeTracks: 4,
        libraryTracks: 8,
        lastSyncedAt: null,
      });
    }
    if (url.includes('/search?'))
      return json({ tracks: [{ id: 's1', title: 'Рассвет', artist: 'Поле', durationSec: 180 }] });
    return json({ code: 'NOT_FOUND', message: 'not found' }, 404);
  });
});

function renderAt(path: string) {
  return render(<App routing="memory" initialPath={path} />);
}

it('refuses a non-admin and does not load product data', async () => {
  refused = true;
  renderAt('/');
  expect(await screen.findByRole('heading', { name: ru.refusedTitle })).toBeInTheDocument();
  expect(screen.getByText(ru.refusedBody)).toBeInTheDocument();
  const paths = calls.map((call) => call.split(' ')[1] ?? '');
  expect(paths.some((path) => path.includes('/admin/users'))).toBe(false);
  expect(paths.some((path) => path.includes('/admin/downloads'))).toBe(false);
  expect(paths.some((path) => path.includes('/admin/health'))).toBe(false);
  expect(paths.some((path) => path.includes('/admin/catalog'))).toBe(false);
});

it('renders service health from the admin API', async () => {
  renderAt('/');
  expect(await screen.findByText('Шлюз')).toBeInTheDocument();
  expect(screen.getByText('Рекомендации')).toBeInTheDocument();
  expect(screen.getByText('не подключён')).toBeInTheDocument();
  expect(screen.getByText('не отвечает')).toBeInTheDocument();
});

it('renders people and their listening preferences', async () => {
  renderAt('/users');
  fireEvent.click(await screen.findByRole('link', { name: /Анна Иванова/ }));
  expect(await screen.findByText('Лунная')).toBeInTheDocument();
  expect(screen.getByText('Поле')).toBeInTheDocument();
  expect(screen.getByText('инди')).toBeInTheDocument();
  expect(screen.queryByText(/accessToken|refreshToken|photoUrl/)).not.toBeInTheDocument();
});

it('lists hidden downloads and confirms before removing a finished file', async () => {
  renderAt('/downloads');
  expect(await screen.findByText('Скрытый файл')).toBeInTheDocument();
  expect(screen.getByText(ru.hiddenNote)).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: ru.removeJob }));
  expect(calls.some((call) => call.startsWith('DELETE'))).toBe(false);
  fireEvent.click(screen.getByRole('button', { name: ru.confirm }));
  await waitFor(() =>
    expect(calls.some((call) => call.startsWith('DELETE') && call.includes('/admin/downloads/job-done'))).toBe(true),
  );
});

it('confirms before retrying a failed download', async () => {
  renderAt('/downloads');
  fireEvent.click(await screen.findByRole('button', { name: ru.retryJob }));
  expect(calls.some((call) => call.startsWith('POST') && call.includes('/retry'))).toBe(false);
  fireEvent.click(screen.getByRole('button', { name: ru.confirm }));
  await waitFor(() =>
    expect(calls.some((call) => call.startsWith('POST') && call.includes('/admin/downloads/job-failed/retry'))).toBe(
      true,
    ),
  );
});

it('renders catalog health and search hits from the existing catalog', async () => {
  renderAt('/music');
  expect(await screen.findByText('12')).toBeInTheDocument();
  expect(screen.getByText(ru.searchHint)).toBeInTheDocument();
  fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'рассвет' } });
  fireEvent.submit(screen.getByRole('search'));
  expect(await screen.findByText('Рассвет')).toBeInTheDocument();
});
