import { ApiError } from './client';

/** i18n key describing an error for the user (exported for tests). */
export function errorMessageKey(error: unknown): string {
  if (!(error instanceof ApiError)) return 'error.generic';
  if (error.status === 0) return 'error.network';
  if (error.status === 501) return 'error.notDeployed';
  if (error.status === 503) return 'error.unavailable';
  return 'error.generic';
}
