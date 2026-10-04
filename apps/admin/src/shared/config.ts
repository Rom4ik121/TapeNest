export const config = {
  apiUrl: (import.meta.env.VITE_API_URL ?? '').trim().replace(/\/+$/, ''),
} as const;
