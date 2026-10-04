import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router-dom';
import { api, apiForm } from '@/shared/api/client';
import { qk } from '@/shared/api/queryClient';
import { EditorTabs } from '@/shared/ui/EditorTabs';
import { moveClip, removeClip, splitClip, TRANSITIONS, type VideoClip } from './ops';

interface VideoText {
  text: string;
  startSec: number;
  endSec: number;
  x: number;
  y: number;
  size: number;
  color: string;
}

interface Recipe {
  clips: VideoClip[];
  texts: VideoText[];
  musicVolume: number;
  muteSource: boolean;
}

interface Project {
  id: string;
  title: string;
  durationSec: number;
  recipe: Recipe;
  hasMusic: boolean;
  export?: { id: string; status: string; error?: string };
}

type Tab = 'clip' | 'timing' | 'look' | 'audio' | 'text' | 'export';
const TABS: Tab[] = ['clip', 'timing', 'look', 'audio', 'text', 'export'];

function num(v: string, fallback: number): number {
  const n = Number(v);
  return Number.isFinite(n) ? n : fallback;
}

export function VideoEditorPage() {
  const { jobId = '' } = useParams();
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [tab, setTab] = useState<Tab>('clip');
  const [index, setIndex] = useState(0);
  const project = useQuery({
    queryKey: qk.project(jobId),
    queryFn: () => api<Project>('/video/projects', { method: 'POST', body: { sourceJobId: jobId } }),
  });
  const save = useMutation({
    mutationFn: (recipe: Recipe) => api<Project>(`/video/projects/${project.data?.id ?? ''}`, { method: 'PUT', body: { recipe } }),
    onSuccess: (next) => qc.setQueryData(qk.project(jobId), next),
  });
  if (project.isLoading) return <p className="text-sm text-muted">{t('auth.loading')}</p>;
  if (project.isError || !project.data) return <p className="text-sm text-danger">{t('common.error')}</p>;
  const data = project.data;
  const clip = data.recipe.clips[Math.min(index, data.recipe.clips.length - 1)] ?? data.recipe.clips[0];
  const patch = (recipe: Recipe) => save.mutate(recipe);
  const patchClip = (next: VideoClip) => {
    const clips = data.recipe.clips.map((c, i) => (i === index ? next : c));
    patch({ ...data.recipe, clips });
  };
  return (
    <div className="flex flex-col gap-3 pb-8">
      <h1 className="truncate text-xl font-bold">{data.title || t('library.videos')}</h1>
      <EditorTabs tabs={TABS.map((id) => ({ id, label: t(`video.${id}`) }))} value={tab} onChange={(id) => setTab(id as Tab)} />
      <div role="tabpanel" id={`panel-${tab}`} aria-labelledby={`tab-${tab}`} className="flex flex-col gap-3">
        {tab === 'clip' && (
          <ClipTab
            clips={data.recipe.clips}
            index={index}
            onPick={setIndex}
            onChange={(clips, nextIndex) => {
              setIndex(nextIndex);
              patch({ ...data.recipe, clips });
            }}
          />
        )}
        {tab === 'timing' && clip && (
          <TimingTab clip={clip} onChange={patchClip} />
        )}
        {tab === 'look' && clip && <LookTab clip={clip} onChange={patchClip} />}
        {tab === 'audio' && (
          <AudioTab
            recipe={data.recipe}
            clipIndex={Math.min(index, data.recipe.clips.length - 1)}
            hasMusic={data.hasMusic}
            projectId={data.id}
            onChange={patch}
            onMusic={() => void qc.invalidateQueries({ queryKey: qk.project(jobId) })}
          />
        )}
        {tab === 'text' && <TextTab recipe={data.recipe} duration={data.durationSec} onChange={patch} />}
        {tab === 'export' && <ExportTab projectId={data.id} current={data.export} />}
      </div>
      {save.isError && <p className="text-sm text-danger">{t('common.error')}</p>}
      {save.isSuccess && tab !== 'export' && <p className="text-xs text-muted">{t('video.saved')}</p>}
    </div>
  );
}

function ClipTab({
  clips,
  index,
  onPick,
  onChange,
}: {
  clips: VideoClip[];
  index: number;
  onPick: (i: number) => void;
  onChange: (clips: VideoClip[], index: number) => void;
}) {
  const { t } = useTranslation();
  const clip = clips[index];
  return (
    <div className="flex flex-col gap-3">
      <ul className="flex flex-col gap-2">
        {clips.map((c, i) => (
          <li key={`${c.startSec}-${c.endSec}-${i}`}>
            <button
              type="button"
              className={`flex min-h-11 w-full items-center justify-between rounded-2xl px-3 text-left ${i === index ? 'bg-accent text-accent-fg' : 'bg-surface'}`}
              onClick={() => onPick(i)}
            >
              <span>
                {i + 1}. {c.startSec.toFixed(1)}–{c.endSec.toFixed(1)} с
              </span>
              <span className="text-xs">{c.speed}×</span>
            </button>
          </li>
        ))}
      </ul>
      {clip && (
        <div className="grid grid-cols-2 gap-2">
          <Field label={t('video.start')} value={clip.startSec} onChange={(startSec) => onChange(clips.map((c, i) => (i === index ? { ...c, startSec } : c)), index)} />
          <Field label={t('video.end')} value={clip.endSec} onChange={(endSec) => onChange(clips.map((c, i) => (i === index ? { ...c, endSec } : c)), index)} />
        </div>
      )}
      <div className="grid grid-cols-2 gap-2">
        <Btn onClick={() => onChange(splitClip(clips, index), index)}>{t('video.split')}</Btn>
        <Btn onClick={() => onChange(removeClip(clips, index), Math.max(0, index - 1))}>{t('video.delete')}</Btn>
        <Btn onClick={() => onChange(moveClip(clips, index, -1), Math.max(0, index - 1))}>{t('video.up')}</Btn>
        <Btn onClick={() => onChange(moveClip(clips, index, 1), Math.min(clips.length - 1, index + 1))}>{t('video.down')}</Btn>
      </div>
    </div>
  );
}

function TimingTab({ clip, onChange }: { clip: VideoClip; onChange: (c: VideoClip) => void }) {
  const { t } = useTranslation();
  return (
    <label className="flex flex-col gap-2 text-sm">
      {t('video.speed')} · {clip.speed.toFixed(2)}×
      <input
        type="range"
        min={0.25}
        max={4}
        step={0.05}
        value={clip.speed}
        className="min-h-11"
        onChange={(e) => onChange({ ...clip, speed: num(e.target.value, clip.speed) })}
      />
    </label>
  );
}

function LookTab({ clip, onChange }: { clip: VideoClip; onChange: (c: VideoClip) => void }) {
  const { t } = useTranslation();
  const turns: VideoClip['rotate'][] = [0, 90, 180, 270];
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm font-semibold">{t('video.rotate')}</p>
      <div className="grid grid-cols-4 gap-2">
        {turns.map((deg) => (
          <button
            key={deg}
            type="button"
            className={`min-h-11 rounded-full text-sm font-semibold ${clip.rotate === deg ? 'bg-accent text-accent-fg' : 'bg-surface-2'}`}
            onClick={() => onChange({ ...clip, rotate: deg })}
          >
            {deg}°
          </button>
        ))}
      </div>
      <p className="text-sm font-semibold">{t('video.crop')}</p>
      {(['x', 'y', 'w', 'h'] as const).map((key) => (
        <label key={key} className="flex flex-col text-xs text-muted">
          {key} · {clip.crop[key].toFixed(2)}
          <input
            type="range"
            min={key === 'w' || key === 'h' ? 0.05 : 0}
            max={1}
            step={0.01}
            value={clip.crop[key]}
            className="min-h-11"
            onChange={(e) => onChange({ ...clip, crop: { ...clip.crop, [key]: num(e.target.value, clip.crop[key]) } })}
          />
        </label>
      ))}
      <label className="flex flex-col gap-1 text-sm">
        {t('video.transition')}
        <select
          className="min-h-11 rounded-2xl border border-line bg-surface px-3 text-base"
          value={clip.transition}
          onChange={(e) => onChange({ ...clip, transition: e.target.value })}
        >
          {TRANSITIONS.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}

function AudioTab({
  recipe,
  clipIndex,
  hasMusic,
  projectId,
  onChange,
  onMusic,
}: {
  recipe: Recipe;
  clipIndex: number;
  hasMusic: boolean;
  projectId: string;
  onChange: (r: Recipe) => void;
  onMusic: () => void;
}) {
  const { t } = useTranslation();
  const clip = recipe.clips[clipIndex];
  return (
    <div className="flex flex-col gap-3">
      <label className="flex min-h-11 items-center gap-3 text-sm">
        <input type="checkbox" checked={recipe.muteSource} onChange={(e) => onChange({ ...recipe, muteSource: e.target.checked })} />
        {t('video.mute')}
      </label>
      {clip && (
        <label className="text-sm">
          {t('video.volume')} · {clip.volume.toFixed(2)}
          <input
            type="range"
            min={0}
            max={2}
            step={0.05}
            value={clip.volume}
            className="min-h-11 w-full"
            onChange={(e) =>
              onChange({
                ...recipe,
                clips: recipe.clips.map((c, i) => (i === clipIndex ? { ...c, volume: num(e.target.value, c.volume) } : c)),
              })
            }
          />
        </label>
      )}
      <label className="text-sm">
        {t('video.musicVolume')} · {recipe.musicVolume.toFixed(2)}
        <input
          type="range"
          min={0}
          max={1}
          step={0.05}
          value={recipe.musicVolume}
          className="min-h-11 w-full"
          onChange={(e) => onChange({ ...recipe, musicVolume: num(e.target.value, recipe.musicVolume) })}
        />
      </label>
      <label className="inline-flex min-h-11 cursor-pointer items-center justify-center rounded-full bg-surface-2 px-4 font-semibold">
        {t('video.music')}
        <input
          type="file"
          accept="audio/*"
          className="sr-only"
          onChange={(e) => {
            const file = e.target.files?.[0];
            if (!file) return;
            const body = new FormData();
            body.append('file', file);
            void apiForm(`/video/projects/${projectId}/music`, body).then(onMusic);
            e.target.value = '';
          }}
        />
      </label>
      {hasMusic && (
        <button
          type="button"
          className="min-h-11 rounded-full bg-surface-2 font-semibold"
          onClick={() => {
            void api(`/video/projects/${projectId}/music`, { method: 'DELETE' }).then(onMusic);
          }}
        >
          {t('video.clearMusic')}
        </button>
      )}
    </div>
  );
}

function TextTab({ recipe, duration, onChange }: { recipe: Recipe; duration: number; onChange: (r: Recipe) => void }) {
  const { t } = useTranslation();
  const text = recipe.texts[0] ?? { text: '', startSec: 0, endSec: Math.min(3, duration), x: 0.5, y: 0.85, size: 36, color: '#FFFFFF' };
  const set = (next: VideoText) => onChange({ ...recipe, texts: next.text.trim() ? [next] : [] });
  return (
    <div className="flex flex-col gap-3">
      <label className="text-sm">
        {t('video.caption')}
        <input
          value={text.text}
          maxLength={80}
          className="mt-1 min-h-11 w-full rounded-2xl border border-line bg-surface px-3 text-base"
          onChange={(e) => set({ ...text, text: e.target.value })}
        />
      </label>
      <Field label={t('video.start')} value={text.startSec} onChange={(startSec) => set({ ...text, startSec })} />
      <Field label={t('video.end')} value={text.endSec} onChange={(endSec) => set({ ...text, endSec })} />
    </div>
  );
}

function ExportTab({ projectId, current }: { projectId: string; current?: Project['export'] }) {
  const { t } = useTranslation();
  const [exportId, setExportId] = useState(current?.id ?? '');
  const status = useQuery({
    queryKey: ['mediahub', 'export', exportId],
    enabled: exportId !== '',
    queryFn: ({ signal }) => api<{ id: string; status: string; error?: string }>(`/video/exports/${exportId}`, { signal }),
    refetchInterval: (q) => (q.state.data && (q.state.data.status === 'queued' || q.state.data.status === 'running') ? 1500 : false),
  });
  const start = useMutation({
    mutationFn: () => api<{ id: string }>(`/video/projects/${projectId}/exports`, { method: 'POST' }),
    onSuccess: (row) => setExportId(row.id),
  });
  const file = useQuery({
    queryKey: ['mediahub', 'export-file', exportId],
    enabled: status.data?.status === 'done',
    queryFn: () => api<{ url: string }>(`/video/exports/${exportId}/file`, { query: { redirect: 'false' } }),
  });
  const st = status.data?.status;
  return (
    <div className="flex flex-col gap-3">
      <button
        type="button"
        disabled={start.isPending || st === 'queued' || st === 'running'}
        className="min-h-11 rounded-full bg-accent font-semibold text-accent-fg disabled:opacity-50"
        onClick={() => start.mutate()}
      >
        {st === 'queued' || st === 'running' ? t('video.rendering') : t('video.render')}
      </button>
      {st === 'failed' && <p className="text-sm text-danger">{status.data?.error || t('common.error')}</p>}
      {file.data?.url && (
        <a className="inline-flex min-h-11 items-center justify-center rounded-full bg-surface-2 font-semibold" href={file.data.url}>
          {t('video.download')}
        </a>
      )}
    </div>
  );
}

function Field({ label, value, onChange }: { label: string; value: number; onChange: (n: number) => void }) {
  return (
    <label className="flex flex-col text-sm">
      {label}
      <input
        type="number"
        step="0.1"
        value={value}
        className="min-h-11 rounded-2xl border border-line bg-surface px-3 text-base"
        onChange={(e) => onChange(num(e.target.value, value))}
      />
    </label>
  );
}

function Btn({ children, onClick }: { children: string; onClick: () => void }) {
  return (
    <button type="button" className="min-h-11 rounded-full bg-surface-2 px-3 text-sm font-semibold" onClick={onClick}>
      {children}
    </button>
  );
}
