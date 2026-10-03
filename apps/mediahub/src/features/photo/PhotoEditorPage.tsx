import { useMutation, useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router-dom';
import { api } from '@/shared/api/client';
import { qk } from '@/shared/api/queryClient';
import { EditorTabs } from '@/shared/ui/EditorTabs';
import { FILTERS, identityPhoto, type PhotoRecipe } from './recipe';

interface PhotoItem {
  id: string;
  title: string;
  width: number;
  height: number;
}

type Tab = 'crop' | 'look' | 'color' | 'text' | 'export';
const TABS: Tab[] = ['crop', 'look', 'color', 'text', 'export'];

function num(v: string, fallback: number): number {
  const n = Number(v);
  return Number.isFinite(n) ? n : fallback;
}

export function PhotoEditorPage() {
  const { photoId = '' } = useParams();
  const { t } = useTranslation();
  const [tab, setTab] = useState<Tab>('crop');
  const [recipe, setRecipe] = useState<PhotoRecipe>(identityPhoto);
  const photo = useQuery({
    queryKey: qk.photo(photoId),
    queryFn: ({ signal }) => api<PhotoItem>(`/photos/${photoId}`, { signal }),
  });
  const original = useQuery({
    queryKey: ['mediahub', 'photo-file', photoId],
    queryFn: () => api<{ url: string }>(`/photos/${photoId}/file`, { query: { redirect: 'false' } }),
  });
  if (photo.isLoading) return <p className="text-sm text-muted">{t('auth.loading')}</p>;
  if (photo.isError || !photo.data) return <p className="text-sm text-danger">{t('common.error')}</p>;
  return (
    <div className="flex flex-col gap-3 pb-8">
      <h1 className="truncate text-xl font-bold">{photo.data.title}</h1>
      {original.data?.url && (
        <img src={original.data.url} alt="" className="max-h-64 w-full rounded-2xl object-contain bg-surface" />
      )}
      <EditorTabs tabs={TABS.map((id) => ({ id, label: t(`photo.${id}`) }))} value={tab} onChange={(id) => setTab(id as Tab)} />
      <div role="tabpanel" id={`panel-${tab}`} aria-labelledby={`tab-${tab}`}>
        {tab === 'crop' && <CropTab recipe={recipe} onChange={setRecipe} />}
        {tab === 'look' && <LookTab recipe={recipe} onChange={setRecipe} />}
        {tab === 'color' && <ColorTab recipe={recipe} onChange={setRecipe} />}
        {tab === 'text' && <TextTab recipe={recipe} onChange={setRecipe} />}
        {tab === 'export' && <ExportTab photoId={photo.data.id} recipe={recipe} />}
      </div>
    </div>
  );
}

function CropTab({ recipe, onChange }: { recipe: PhotoRecipe; onChange: (r: PhotoRecipe) => void }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col gap-2">
      <p className="text-sm font-semibold">{t('photo.crop')}</p>
      {(['x', 'y', 'w', 'h'] as const).map((key) => (
        <label key={key} className="text-xs text-muted">
          {key} · {recipe.crop[key].toFixed(2)}
          <input
            type="range"
            min={key === 'w' || key === 'h' ? 0.05 : 0}
            max={1}
            step={0.01}
            value={recipe.crop[key]}
            className="min-h-11 w-full"
            onChange={(e) => onChange({ ...recipe, crop: { ...recipe.crop, [key]: num(e.target.value, recipe.crop[key]) } })}
          />
        </label>
      ))}
    </div>
  );
}

function LookTab({ recipe, onChange }: { recipe: PhotoRecipe; onChange: (r: PhotoRecipe) => void }) {
  const { t } = useTranslation();
  const turns: PhotoRecipe['rotate'][] = [0, 90, 180, 270];
  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-4 gap-2">
        {turns.map((deg) => (
          <button
            key={deg}
            type="button"
            className={`min-h-11 rounded-full text-sm font-semibold ${recipe.rotate === deg ? 'bg-accent text-accent-fg' : 'bg-surface-2'}`}
            onClick={() => onChange({ ...recipe, rotate: deg })}
          >
            {deg}°
          </button>
        ))}
      </div>
      <label className="text-sm">
        {t('photo.straighten')} · {recipe.straighten.toFixed(0)}°
        <input
          type="range"
          min={-45}
          max={45}
          step={1}
          value={recipe.straighten}
          className="min-h-11 w-full"
          onChange={(e) => onChange({ ...recipe, straighten: num(e.target.value, 0) })}
        />
      </label>
      <label className="text-sm">
        {t('photo.exposure')} · {recipe.exposure.toFixed(2)}
        <input
          type="range"
          min={-2}
          max={2}
          step={0.05}
          value={recipe.exposure}
          className="min-h-11 w-full"
          onChange={(e) => onChange({ ...recipe, exposure: num(e.target.value, 0) })}
        />
      </label>
    </div>
  );
}

function ColorTab({ recipe, onChange }: { recipe: PhotoRecipe; onChange: (r: PhotoRecipe) => void }) {
  const { t } = useTranslation();
  const sliders = [
    ['contrast', 'photo.contrast'],
    ['saturation', 'photo.saturation'],
    ['temperature', 'photo.temperature'],
  ] as const;
  return (
    <div className="flex flex-col gap-3">
      {sliders.map(([key, label]) => (
        <label key={key} className="text-sm">
          {t(label)} · {recipe[key].toFixed(2)}
          <input
            type="range"
            min={-1}
            max={1}
            step={0.05}
            value={recipe[key]}
            className="min-h-11 w-full"
            onChange={(e) => onChange({ ...recipe, [key]: num(e.target.value, 0) })}
          />
        </label>
      ))}
      <label className="text-sm">
        {t('photo.filter')}
        <select
          className="mt-1 min-h-11 w-full rounded-2xl border border-line bg-surface px-3 text-base"
          value={recipe.filter}
          onChange={(e) => {
            const filter = e.target.value;
            if (FILTERS.includes(filter as PhotoRecipe['filter'])) onChange({ ...recipe, filter: filter as PhotoRecipe['filter'] });
          }}
        >
          {FILTERS.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </select>
      </label>
    </div>
  );
}

function TextTab({ recipe, onChange }: { recipe: PhotoRecipe; onChange: (r: PhotoRecipe) => void }) {
  const { t } = useTranslation();
  return (
    <label className="text-sm">
      {t('photo.text')}
      <input
        value={recipe.text.text}
        maxLength={80}
        className="mt-1 min-h-11 w-full rounded-2xl border border-line bg-surface px-3 text-base"
        onChange={(e) => onChange({ ...recipe, text: { ...recipe.text, text: e.target.value } })}
      />
    </label>
  );
}

function ExportTab({ photoId, recipe }: { photoId: string; recipe: PhotoRecipe }) {
  const { t } = useTranslation();
  const [format, setFormat] = useState<PhotoRecipe['format']>(recipe.format);
  const render = useMutation({
    mutationFn: () =>
      api<{ url: string }>(`/photos/${photoId}/exports`, { method: 'POST', body: { recipe: { ...recipe, format } } }),
  });
  return (
    <div className="flex flex-col gap-3">
      <div className="grid grid-cols-2 gap-2">
        {(['jpeg', 'png'] as const).map((fmt) => (
          <button
            key={fmt}
            type="button"
            className={`min-h-11 rounded-full text-sm font-semibold ${format === fmt ? 'bg-accent text-accent-fg' : 'bg-surface-2'}`}
            onClick={() => setFormat(fmt)}
          >
            {fmt}
          </button>
        ))}
      </div>
      <button
        type="button"
        disabled={render.isPending}
        className="min-h-11 rounded-full bg-accent font-semibold text-accent-fg disabled:opacity-50"
        onClick={() => render.mutate()}
      >
        {render.isPending ? t('photo.rendering') : t('photo.render')}
      </button>
      {render.isError && <p className="text-sm text-danger">{t('common.error')}</p>}
      {render.data?.url && (
        <a className="inline-flex min-h-11 items-center justify-center rounded-full bg-surface-2 font-semibold" href={render.data.url}>
          {t('video.download')}
        </a>
      )}
    </div>
  );
}
