export const config = {
  /** api-gateway origin without /api/v1. Empty = same origin (Vite proxy / nginx). */
  apiUrl: (import.meta.env.VITE_API_URL ?? '').trim().replace(/\/+$/, ''),
  botUsername: (import.meta.env.VITE_BOT_USERNAME ?? '').trim().replace(/^@/, ''),
  useMocks: false,
  isMocked: (_domain: 'auth'): boolean => false,
} as const;
