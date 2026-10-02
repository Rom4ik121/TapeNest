import { Check, FolderPlus, Heart, ListMusic, Play, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { playerStore } from '@/entities/player';
import type { Track } from '@/shared/api/types';
import { haptic } from '@/shared/telegram';
import { Cover } from '@/shared/ui/Cover';
import { Sheet } from '@/shared/ui/Sheet';
import { usePlaylistMutations, usePlaylists } from './queries';

interface Props {
  track: Track | null;
  onClose: () => void;
  onRemove?: (track: Track) => void;
  removeLabel?: string;
}

export function AddToPlaylistSheet({ track, onClose, onRemove, removeLabel }: Props) {
  const { t } = useTranslation();
  const playlists = usePlaylists();
  const { addTrack, create } = usePlaylistMutations();
  const [added, setAdded] = useState<string | null>(null);
  const [newTitle, setNewTitle] = useState('');

  const close = (): void => {
    setAdded(null);
    setNewTitle('');
    onClose();
  };

  const add = (id: string, title: string): void => {
    if (!track) return;
    addTrack.mutate(
      { id, trackId: track.id },
      {
        onSuccess: () => {
          haptic('success');
          setAdded(title);
          setTimeout(close, 700);
        },
      },
    );
  };

  const createAndAdd = (): void => {
    const title = newTitle.trim();
    if (!title) return;
    create.mutate(title, { onSuccess: (p) => add(p.id, p.title) });
  };

  const btn = 'flex w-full items-center gap-3 rounded-2xl px-3 py-3 text-left font-medium active:bg-fg/5';

  return (
    <Sheet open={track !== null} onClose={close}>
      {track && (
        <div className="pb-2">
          <div className="mb-3 flex items-center gap-3 px-2">
            <Cover id={track.id} src={track.coverUrl} alt="" className="h-12 w-12" />
            <div className="min-w-0">
              <p className="truncate font-semibold">{track.title}</p>
              <p className="truncate text-sm text-muted">{track.artist}</p>
            </div>
          </div>
          {added ? (
            <p className="flex items-center gap-2 px-3 py-4 font-semibold text-accent-ink" role="status">
              <Check className="h-5 w-5" /> {t('track.added', { title: added })}
            </p>
          ) : (
            <>
              <button type="button" className={btn} onClick={() => (playerStore.getState().playTrack(track), close())}>
                <Play className="h-5 w-5 text-muted" /> {t('track.playNext')}
              </button>
              <button
                type="button"
                className={btn}
                onClick={() => (playerStore.getState().setLiked(track.id, !track.liked), close())}
              >
                <Heart className="h-5 w-5 text-muted" /> {track.liked ? t('track.unlike') : t('track.like')}
              </button>
              {onRemove && (
                <button type="button" className={`${btn} text-danger`} onClick={() => (onRemove(track), close())}>
                  <Trash2 className="h-5 w-5" /> {removeLabel}
                </button>
              )}
              <p className="mt-3 px-3 text-xs font-bold uppercase tracking-wider text-muted">
                {t('track.addToPlaylist')}
              </p>
              <div className="max-h-60 overflow-y-auto">
                {playlists.data?.map((p) => (
                  <button key={p.id} type="button" className={btn} onClick={() => add(p.id, p.title)}>
                    <ListMusic className="h-5 w-5 text-muted" />
                    <span className="flex-1 truncate">{p.title}</span>
                    <span className="text-xs text-muted">{t('library.tracks', { count: p.trackCount })}</span>
                  </button>
                ))}
              </div>
              <form
                className="mt-2 flex items-center gap-2 px-1"
                onSubmit={(e) => {
                  e.preventDefault();
                  createAndAdd();
                }}
              >
                <FolderPlus className="ml-2 h-5 w-5 shrink-0 text-muted" />
                <input
                  value={newTitle}
                  onChange={(e) => setNewTitle(e.target.value)}
                  maxLength={100}
                  placeholder={t('library.newPlaylist')}
                  className="h-11 min-w-0 flex-1 rounded-xl bg-fg/5 px-3 outline-none placeholder:text-muted focus:ring-2 focus:ring-accent/40"
                />
                <button
                  type="submit"
                  disabled={!newTitle.trim() || create.isPending}
                  className="h-11 rounded-xl bg-accent px-4 text-sm font-semibold text-accent-fg disabled:opacity-40"
                >
                  {t('library.create')}
                </button>
              </form>
            </>
          )}
        </div>
      )}
    </Sheet>
  );
}
