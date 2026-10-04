export const FILTERS = ['none', 'vivid', 'mono', 'warm', 'cool', 'fade'] as const;
export type PhotoFilter = (typeof FILTERS)[number];

export interface PhotoRecipe {
  crop: { x: number; y: number; w: number; h: number };
  rotate: 0 | 90 | 180 | 270;
  straighten: number;
  exposure: number;
  contrast: number;
  saturation: number;
  temperature: number;
  filter: PhotoFilter;
  text: { text: string; x: number; y: number; size: number; color: string };
  format: 'jpeg' | 'png';
}

export function identityPhoto(): PhotoRecipe {
  return {
    crop: { x: 0, y: 0, w: 1, h: 1 },
    rotate: 0,
    straighten: 0,
    exposure: 0,
    contrast: 0,
    saturation: 0,
    temperature: 0,
    filter: 'none',
    text: { text: '', x: 0.5, y: 0.85, size: 0.06, color: '#FFFFFF' },
    format: 'jpeg',
  };
}
