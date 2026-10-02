import { Music2 } from 'lucide-react';
import { useState } from 'react';
import { cn } from '@/shared/lib/cn';
import { gradientFor } from '@/shared/lib/format';

interface Props {
  id: string;
  src: string | null;
  alt: string;
  className?: string;
  rounded?: string;
}

export function Cover({ id, src, alt, className, rounded = 'rounded-xl' }: Props) {
  const [failed, setFailed] = useState(false);
  return (
    <div className={cn('relative shrink-0 overflow-hidden', rounded, gradientFor(id), className)}>
      {src && !failed ? (
        <img
          src={src}
          alt={alt}
          loading="lazy"
          decoding="async"
          draggable={false}
          onError={() => setFailed(true)}
          className="h-full w-full object-cover"
        />
      ) : (
        <Music2 className="absolute inset-0 m-auto h-1/3 w-1/3 text-cream/80" aria-hidden />
      )}
    </div>
  );
}
