import type { ReactNode } from 'react';

export function SectionHeader({ title, action }: { title: string; action?: ReactNode }) {
  return (
    <div className="mb-2 mt-7 flex items-end justify-between px-1">
      <h2 className="text-xl font-extrabold tracking-tight">{title}</h2>
      {action}
    </div>
  );
}
