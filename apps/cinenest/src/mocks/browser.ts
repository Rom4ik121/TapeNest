import { setupWorker } from 'msw/browser';
import type { RequestHandler } from 'msw';

export function createWorker(handlers: RequestHandler[]) {
  return setupWorker(...handlers);
}
