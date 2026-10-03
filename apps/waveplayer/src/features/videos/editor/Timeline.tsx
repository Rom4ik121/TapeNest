import { useRef, type PointerEvent as ReactPointerEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { outputSeconds, projectDuration, spans, trimClip, type Project } from './model';

const SWATCH = ['bg-plum', 'bg-teal', 'bg-magenta'] as const;

interface TimelineProps {
  project: Project;
  time: number;
  selectedId: string;
  onSelect: (id: string) => void;
  onSeek: (time: number) => void;
  onPreview: (project: Project) => void;
  onCommit: (project: Project) => void;
}

export function Timeline({ project, time, selectedId, onSelect, onSeek, onPreview, onCommit }: TimelineProps) {
  const { t } = useTranslation();
  const trackRef = useRef<HTMLDivElement>(null);
  const total = Math.max(projectDuration(project), 0.1);
  const layout = spans(project);

  const nudge = (clipId: string, edge: 'in' | 'out', delta: number) => {
    const clip = project.clips.find((item) => item.id === clipId);
    if (!clip) return;
    const origin = edge === 'in' ? clip.inSec : clip.outSec;
    onCommit(trimClip(project, clipId, edge, origin + delta));
  };

  const beginTrim = (clipId: string, edge: 'in' | 'out', event: ReactPointerEvent<HTMLButtonElement>) => {
    event.stopPropagation();
    const clip = project.clips.find((item) => item.id === clipId);
    const bounds = trackRef.current?.getBoundingClientRect();
    if (!clip || !bounds || bounds.width < 8) return;
    const origin = edge === 'in' ? clip.inSec : clip.outSec;
    const startX = event.clientX;
    const apply = (clientX: number) => {
      const delta = ((clientX - startX) / bounds.width) * total * clip.speed;
      return trimClip(project, clipId, edge, origin + delta);
    };
    const move = (ev: PointerEvent) => onPreview(apply(ev.clientX));
    const up = (ev: PointerEvent) => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
      onCommit(apply(ev.clientX));
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
  };

  return (
    <section className="rounded-3xl bg-surface p-3 shadow-card">
      <input
        type="range"
        aria-label={t('videos.editor.playhead')}
        min={0}
        max={total}
        step={0.1}
        value={Math.min(time, total)}
        onChange={(event) => onSeek(Number(event.target.value))}
        className="mb-3 h-2 w-full accent-amber"
      />
      <div
        ref={trackRef}
        role="list"
        aria-label={t('videos.editor.timeline')}
        className="flex gap-1 overflow-x-auto pb-1"
      >
        {project.clips.map((clip, index) => {
          const selected = clip.id === selectedId;
          const title = clip.title.trim() || t('videos.untitled');
          const width = Math.max(88, outputSeconds(clip) * 36);
          return (
            <div key={clip.id} role="listitem" className="relative shrink-0" style={{ width }}>
              <button
                type="button"
                aria-pressed={selected}
                onClick={() => {
                  onSelect(clip.id);
                  onSeek(layout[index]?.start ?? 0);
                }}
                className={`h-16 w-full overflow-hidden rounded-xl px-3 text-left text-[11px] font-semibold text-cream ${SWATCH[index % SWATCH.length]} ${selected ? 'ring-2 ring-amber' : ''}`}
                style={
                  clip.posterUrl
                    ? {
                        backgroundImage: `linear-gradient(#16141C99, #16141C99), url(${clip.posterUrl})`,
                        backgroundSize: 'cover',
                      }
                    : undefined
                }
              >
                <span className="line-clamp-2">{t('videos.editor.clip', { n: index + 1, title })}</span>
              </button>
              {selected && (
                <>
                  <button
                    type="button"
                    aria-label={t('videos.editor.trimStart')}
                    onPointerDown={(event) => beginTrim(clip.id, 'in', event)}
                    onKeyDown={(event) => {
                      if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
                      event.preventDefault();
                      nudge(clip.id, 'in', event.key === 'ArrowRight' ? 0.5 : -0.5);
                    }}
                    className="absolute inset-y-1 left-0 w-2.5 cursor-ew-resize rounded-full bg-amber"
                  />
                  <button
                    type="button"
                    aria-label={t('videos.editor.trimEnd')}
                    onPointerDown={(event) => beginTrim(clip.id, 'out', event)}
                    onKeyDown={(event) => {
                      if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
                      event.preventDefault();
                      nudge(clip.id, 'out', event.key === 'ArrowRight' ? 0.5 : -0.5);
                    }}
                    className="absolute inset-y-1 right-0 w-2.5 cursor-ew-resize rounded-full bg-amber"
                  />
                </>
              )}
            </div>
          );
        })}
      </div>
      {project.music && (
        <p className="mt-2 truncate rounded-xl bg-teal/20 px-3 py-2 text-xs font-semibold text-fg">
          {t('videos.editor.musicLabel', { title: project.music.title || t('videos.untitled') })}
        </p>
      )}
    </section>
  );
}
