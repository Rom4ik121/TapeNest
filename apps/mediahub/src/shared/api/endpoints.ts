import { api } from './client';
import type { AuthTokens, User } from './types';

export const authApi = {
  login: (initData: string) => api<AuthTokens>('/auth/telegram', { method: 'POST', body: { initData }, auth: false }),
  refresh: (refreshToken: string) =>
    api<AuthTokens>('/auth/refresh', { method: 'POST', body: { refreshToken }, auth: false }),
  logout: (refreshToken: string) => api<void>('/auth/logout', { method: 'POST', body: { refreshToken }, auth: false }),
  me: (signal?: AbortSignal) => api<User>('/me', { signal }),
};
