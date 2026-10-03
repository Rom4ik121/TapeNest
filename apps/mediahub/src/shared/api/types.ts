/** Auth types shared with api-gateway. */
export interface User {
  id: string;
  telegramId: number;
  firstName: string;
  lastName: string | null;
  username: string | null;
  photoUrl: string | null;
  languageCode: string | null;
  role?: 'user' | 'admin';
}

export interface AuthTokens {
  accessToken: string;
  refreshToken: string;
  expiresIn?: number;
  user: User;
}

export interface ApiErrorBody {
  message?: string;
  code?: string;
  service?: string;
}

export interface Page<T> {
  items: T[];
  nextCursor: string | null;
}
