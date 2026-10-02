/**
 * AbortSignal with a timeout that also follows an optional external signal.
 * Does NOT use AbortSignal.timeout / AbortSignal.any — both are missing in
 * older iOS WebViews (Safari < 16) that Telegram still runs on.
 */
export interface TimedSignal {
  signal: AbortSignal;
  /** Must be called when the request settles (clears the timer/listener). */
  dispose: () => void;
  timedOut: () => boolean;
}

export function createTimedSignal(ms: number, external?: AbortSignal): TimedSignal {
  const controller = new AbortController();
  let didTimeout = false;
  const timer = setTimeout(() => {
    didTimeout = true;
    controller.abort();
  }, ms);

  const onExternalAbort = (): void => controller.abort();
  if (external) {
    if (external.aborted) controller.abort();
    else external.addEventListener('abort', onExternalAbort, { once: true });
  }

  return {
    signal: controller.signal,
    timedOut: () => didTimeout,
    dispose: () => {
      clearTimeout(timer);
      external?.removeEventListener('abort', onExternalAbort);
    },
  };
}
