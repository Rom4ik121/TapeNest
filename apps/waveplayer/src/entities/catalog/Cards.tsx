import { Link } from 'react-router-dom';
import type { Album, Artist } from '@/shared/api/types';
import { Cover } from '@/shared/ui/Cover';
import { haptic } from '@/shared/telegram';

/** Album tile — identical for library and not-yet-available albums (ADR 0011). */
export function AlbumCard({ album }: { album: Album }) {
  return (
    <Link
      to={`/album/${encodeURIComponent(album.id)}`}
      onClick={() => haptic('light')}
      aria-label={`${album.title} — ${album.artist}`}
      className="w-32 shrink-0 rounded-2xl text-left transition active:scale-[0.97]"
    >
      <Cover id={album.id} src={album.coverUrl} alt="" className="h-32 w-32 shadow-card" rounded="rounded-2xl" />
      <p className="mt-2 truncate text-sm font-semibold">{album.title}</p>
      <p className="truncate text-xs text-muted">
        {album.artist}
        {album.year ? ` · ${album.year}` : ''}
      </p>
    </Link>
  );
}

export function ArtistCard({ artist }: { artist: Artist }) {
  return (
    <Link
      to={`/artist/${encodeURIComponent(artist.id)}`}
      onClick={() => haptic('light')}
      className="w-24 shrink-0 text-center transition active:scale-[0.97]"
    >
      <Cover
        id={artist.id}
        src={artist.coverUrl}
        alt=""
        className="mx-auto h-24 w-24 shadow-card"
        rounded="rounded-full"
      />
      <p className="mt-2 line-clamp-2 text-sm font-semibold leading-tight">{artist.name}</p>
    </Link>
  );
}

export function CardRow({ children }: { children: React.ReactNode }) {
  return <div className="no-scrollbar -mx-4 flex gap-3 overflow-x-auto px-4 pb-1">{children}</div>;
}
