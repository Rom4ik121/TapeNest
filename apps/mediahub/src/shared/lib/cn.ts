import { clsx, type ClassValue } from 'clsx';

export const cn = (...v: ClassValue[]): string => clsx(v);
