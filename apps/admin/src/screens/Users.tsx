import { useQuery } from '@tanstack/react-query';
import { Link, useParams } from 'react-router-dom';
import { displayName, roleLabel, ru } from '@/i18n/labels';
import { api } from '@/shared/api/client';
import type { AdminTrack, AdminUser, TasteCount, UserMusic } from '@/shared/api/types';
import { formatWhen } from '@/shared/format';
import { Empty, Failed, Loading } from '@/shared/ui/States';

export function Users() {
  const users = useQuery({
    queryKey: ['admin-users'],
    queryFn: () => api<{ items: AdminUser[] }>('/admin/users'),
  });

  return (
    <section className="space-y-4">
      <h1 className="text-2xl font-semibold">{ru.usersTitle}</h1>
      {users.isPending ? <Loading /> : null}
      {users.isError ? <Failed onRetry={() => void users.refetch()} /> : null}
      {users.data && users.data.items.length === 0 ? <Empty /> : null}
      {users.data && users.data.items.length > 0 ? (
        <ul className="space-y-2">
          {users.data.items.map((user) => (
            <li key={user.id}>
              <Link to={`/users/${user.id}`} className="block rounded-2xl bg-surface px-4 py-3 shadow-card">
                <div className="flex items-baseline justify-between gap-3">
                  <span className="font-semibold">{displayName(user)}</span>
                  <span className="text-xs text-muted">{roleLabel(user.role)}</span>
                </div>
                <p className="mt-1 text-sm text-muted">
                  {user.username ? `@${user.username} · ` : null}
                  id {user.telegramId}
                </p>
              </Link>
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}

export function UserDetail() {
  const { id = '' } = useParams();
  const users = useQuery({
    queryKey: ['admin-users'],
    queryFn: () => api<{ items: AdminUser[] }>('/admin/users'),
  });
  const music = useQuery({
    queryKey: ['admin-user-music', id],
    queryFn: () => api<UserMusic>(`/admin/users/${id}/music`),
    enabled: id.length > 0,
  });
  const user = users.data?.items.find((item) => item.id === id);

  return (
    <section className="space-y-4">
      <Link to="/users" className="text-sm font-semibold text-magenta">
        {ru.back}
      </Link>
      <h1 className="text-2xl font-semibold">{user ? displayName(user) : ru.usersTitle}</h1>
      {user ? (
        <p className="text-sm text-muted">
          {user.username ? `@${user.username} · ` : null}
          Telegram {user.telegramId}
          {user.lastLoginAt ? ` · ${formatWhen(user.lastLoginAt)}` : null}
        </p>
      ) : null}
      {music.isPending ? <Loading /> : null}
      {music.isError ? <Failed onRetry={() => void music.refetch()} /> : null}
      {music.data ? <Taste music={music.data} /> : null}
    </section>
  );
}

function Taste({ music }: { music: UserMusic }) {
  const empty = music.likes.length + music.recent.length + music.artists.length + music.genres.length === 0;
  if (empty) return <Empty text={ru.noPrefs} />;
  return (
    <div className="space-y-4">
      <TrackList title={ru.likes} tracks={music.likes} />
      <TrackList title={ru.recent} tracks={music.recent} />
      <CountList title={ru.artists} items={music.artists} />
      <CountList title={ru.genres} items={music.genres} />
    </div>
  );
}

function TrackList({ title, tracks }: { title: string; tracks: AdminTrack[] }) {
  if (tracks.length === 0) return null;
  return (
    <div>
      <h2 className="mb-2 text-sm font-semibold uppercase tracking-wide text-muted">{title}</h2>
      <ul className="space-y-2">
        {tracks.map((track) => (
          <li key={`${title}-${track.id}`} className="rounded-2xl bg-surface px-4 py-3 shadow-card">
            <p className="font-medium">{track.title}</p>
            <p className="text-sm text-muted">
              {track.artist}
              {track.album ? ` · ${track.album}` : ''}
            </p>
          </li>
        ))}
      </ul>
    </div>
  );
}

function CountList({ title, items }: { title: string; items: TasteCount[] }) {
  if (items.length === 0) return null;
  return (
    <div>
      <h2 className="mb-2 text-sm font-semibold uppercase tracking-wide text-muted">{title}</h2>
      <ul className="flex flex-wrap gap-2">
        {items.map((item) => (
          <li key={`${title}-${item.name}`} className="rounded-full bg-surface-2 px-3 py-1.5 text-sm">
            {item.name}
            <span className="ml-1 text-muted">{item.count}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
