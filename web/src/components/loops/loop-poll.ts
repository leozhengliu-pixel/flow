import { useEffect } from "react";
import { ApiError } from "@/lib/api-client";

const RETRY_BASE_MS = 1000;
const RETRY_MAX_MS = 15000;

/** Wait before the next attempt after `failures` consecutive failures: 1s, 2s, 4s… capped so a long outage is not hammered. */
export function pollRetryDelay(failures: number) {
  return Math.min(RETRY_MAX_MS, RETRY_BASE_MS * 2 ** Math.max(0, failures - 1));
}

/**
 * A failure that retrying will not fix (the record is gone or not visible to the viewer).
 * Network errors and 5xx (an API restart answers 502/503/504 through the dev proxy) are transient.
 */
export function isPermanentLoadError(reason: unknown) {
  if (!(reason instanceof ApiError)) return false;
  return reason.status >= 400 && reason.status < 500 && ![408, 425, 429].includes(reason.status);
}

/** Calls `retry` after an exponentially growing delay while `failures` is above zero; each new failure re-arms it. */
export function useBackoffRetry(failures: number, retry: () => void) {
  useEffect(() => {
    if (failures <= 0) return;
    const timer = window.setTimeout(retry, pollRetryDelay(failures));
    return () => window.clearTimeout(timer);
  }, [failures, retry]);
}
