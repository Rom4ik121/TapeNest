import type { ReactNode } from 'react';

export function SectionHeader({ title, action, id }: { title: string; action?: ReactNode; id?: string }) {
  return (
    <div className="mb-2 mt-7 flex items-end justify-between px-1">
      <h2 id={id} className="text-xl font-extrabold tracking-tight">
        {title}
      </h2>
      {action}
    </div>
  );
}
