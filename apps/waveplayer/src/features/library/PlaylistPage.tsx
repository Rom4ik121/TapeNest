import { ListMusic, Pencil, Play, Shuffle, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate, useParams } from 'react-router-dom';
import { playerStore } from '@/entities/player';
import { usePlaylist, usePlaylistMutations } from '@/entities/track/queries';
import { TrackList } from '@/entities/track/TrackList';
import { cn } from '@/shared/lib/cn';
import { gradientFor } from '@/shared/lib/format';
import { shuffled } from '@/shared/lib/shuffle';
import { Sheet } from '@/shared/ui/Sheet';
import { TrackListSkeleton } from '@/shared/ui/Skeleton';
import { EmptyState, ErrorState } from '@/shared/ui/States';

export function PlaylistPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const id = decodeURIComponent(useParams().playlistId ?? '');
  const q = usePlaylist(id);
  const { rename, remove, removeTrack } = usePlaylistMutations();
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState('');
  const [confirmDelete, setConfirmDelete] = useState(false);

  if (q.isPending) return <TrackListSkeleton />;
  if (q.isError)
    return (
      <ErrorState
        error={q.error}
        title={t('playlist.notFound')}
        onRetry={() => void q.refetch()}
        retrying={q.isFetching}
      />
    );
  const { playlist, tracks } = q.data;

  return (
    <div>
      <div
        className={cn(
          'scrim on-gradient relative -mx-4 -mt-3 mb-4 overflow-hidden px-5 pb-6 pt-10 text-cream',
          gradientFor(playlist.id),
        )}
      >
        <div className="absolute inset-0 bg-gradient-to-b from-transparent to-ink/40" />
        <div className="relative">
          <ListMusic className="h-10 w-10" />
          <h1 className="mt-3 break-words text-3xl font-extrabold tracking-tight">{playlist.title}</h1>
          <p className="mt-1 text-sm text-cream/85">{t('library.tracks', { count: tracks.length })}</p>
          <div className="mt-4 flex items-center gap-2">
            <button
              type="button"
              disabled={tracks.length === 0}
              onClick={() => playerStore.getState().playQueue(tracks, 0)}
              className="inline-flex h-11 items-center gap-2 rounded-full bg-cream px-5 font-semibold text-ink disabled:opacity-50 active:scale-95"
            >
              <Play className="h-4 w-4 fill-current" /> {t('playlist.play')}
            </button>
            <button
              type="button"
              disabled={tracks.length === 0}
              onClick={() => playerStore.getState().playQueue(shuffled(tracks), 0)}
              aria-label={t('playlist.shuffle')}
              className="grid h-11 w-11 place-items-center rounded-full bg-cream/20 disabled:opacity-50 active:scale-95"
            >
              <Shuffle className="h-5 w-5" />
            </button>
            <span className="flex-1" />
            <button
              type="button"
              onClick={() => (setTitle(playlist.title), setEditing(true))}
              aria-label={t('library.rename')}
              className="grid h-11 w-11 place-items-center rounded-full bg-cream/20 active:scale-95"
            >
              <Pencil className="h-5 w-5" />
            </button>
            <button
              type="button"
              onClick={() => setConfirmDelete(true)}
              aria-label={t('library.delete')}
              className="grid h-11 w-11 place-items-center rounded-full bg-cream/20 active:scale-95"
            >
              <Trash2 className="h-5 w-5" />
            </button>
          </div>
        </div>
      </div>

      {tracks.length === 0 ? (
        <EmptyState icon={<ListMusic className="h-10 w-10 opacity-40" />}>{t('playlist.empty')}</EmptyState>
      ) : (
        <TrackList
          tracks={tracks}
          onRemove={(tr) => removeTrack.mutate({ id: playlist.id, trackId: tr.id })}
          removeLabel={t('playlist.remove')}
        />
      )}

      <Sheet open={editing} onClose={() => setEditing(false)} title={t('library.rename')}>
        <form
          className="space-y-3 pb-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (title.trim())
              rename.mutate({ id: playlist.id, title: title.trim() }, { onSuccess: () => setEditing(false) });
          }}
        >
          <input
            autoFocus
            value={title}
            maxLength={100}
            onChange={(e) => setTitle(e.target.value)}
            aria-label={t('library.namePlaceholder')}
            className="h-12 w-full rounded-2xl bg-fg/5 px-4 text-[16px] outline-none focus:ring-2 focus:ring-accent/50"
          />
          <button
            type="submit"
            disabled={!title.trim()}
            className="h-12 w-full rounded-2xl bg-accent font-semibold text-accent-fg disabled:opacity-40"
          >
            {t('library.save')}
          </button>
        </form>
      </Sheet>

      <Sheet
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        title={t('library.deleteConfirm', { title: playlist.title })}
      >
        <div className="flex gap-2 pb-3">
          <button
            type="button"
            onClick={() => setConfirmDelete(false)}
            className="h-12 flex-1 rounded-2xl bg-fg/5 font-semibold"
          >
            {t('library.cancel')}
          </button>
          <button
            type="button"
            onClick={() =>
              remove.mutate(playlist.id, {
                onSuccess: () => navigate('/library', { replace: true }),
              })
            }
            className="h-12 flex-1 rounded-2xl bg-danger font-semibold text-danger-fg"
          >
            {t('library.delete')}
          </button>
        </div>
      </Sheet>
    </div>
  );
}
