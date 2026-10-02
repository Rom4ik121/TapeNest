import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import type { AuthTokens, User } from '@/shared/api/types';

export type AuthStatus = 'idle' | 'loading' | 'ready' | 'error';
export type AuthErrorKind = 'openInTelegram' | 'failed' | 'unavailable';

interface AuthState {
  accessToken: string | null;
  refreshToken: string | null;
  user: User | null;
  /** Who the tokens belong to: `tg:<telegramId>` or `guest` (browser + mocks). */
  identity: string | null;
  status: AuthStatus;
  error: AuthErrorKind | null;
  setSession: (tokens: AuthTokens, identity: string) => void;
  clearSession: () => void;
  setUser: (user: User) => void;
  setStatus: (status: AuthStatus, error?: AuthErrorKind | null) => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      accessToken: null,
      refreshToken: null,
      user: null,
      identity: null,
      status: 'idle',
      error: null,
      setSession: (t, identity) =>
        set({
          accessToken: t.accessToken,
          refreshToken: t.refreshToken,
          user: t.user,
          identity,
        }),
      clearSession: () =>
        set({
          accessToken: null,
          refreshToken: null,
          user: null,
          identity: null,
        }),
      setUser: (user) => set({ user }),
      setStatus: (status, error = null) => set({ status, error }),
    }),
    {
      name: 'waveplayer:auth:v2',
      partialize: (s) => ({
        accessToken: s.accessToken,
        refreshToken: s.refreshToken,
        user: s.user,
        identity: s.identity,
      }),
    },
  ),
);
