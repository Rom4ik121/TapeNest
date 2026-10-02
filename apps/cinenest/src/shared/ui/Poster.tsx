import { Clapperboard } from 'lucide-react';
import { useState } from 'react';
import { cn } from '@/shared/lib/cn';
import { gradientFor } from '@/shared/lib/format';

interface Props {
  id: string;
  src: string | null;
  alt: string;
  className?: string;
  aspect?: 'poster' | 'wide';
}

/** Poster/backdrop with a palette-gradient placeholder while loading or on error. */
export function Poster({ id, src, alt, className, aspect = 'poster' }: Props) {
  const [failed, setFailed] = useState(false);
  const [loaded, setLoaded] = useState(false);
  return (
    <div
      className={cn(
        'relative shrink-0 overflow-hidden',
        aspect === 'poster' ? 'aspect-[2/3]' : 'aspect-video',
        gradientFor(id),
        className,
      )}
    >
      {src && !failed ? (
        <img
          src={src}
          alt={alt}
          loading="lazy"
          decoding="async"
          draggable={false}
          onLoad={() => setLoaded(true)}
          onError={() => setFailed(true)}
          className={cn(
            'h-full w-full object-cover transition-opacity duration-300',
            loaded ? 'opacity-100' : 'opacity-0',
          )}
        />
      ) : (
        <Clapperboard className="absolute inset-0 m-auto h-1/4 w-1/4 text-cream/80" aria-hidden />
      )}
    </div>
  );
}
