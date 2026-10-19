/** Jittered exponential backoff: base·2ⁿ capped at max, with ±50% jitter. */
export function backoffDelay(attempt: number, baseMs = 1000, maxMs = 30_000, random: () => number = Math.random): number {
  const exp = Math.min(maxMs, baseMs * 2 ** Math.max(0, Math.min(attempt, 30)))
  return Math.round(exp / 2 + random() * (exp / 2))
}

export function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason ?? new Error('aborted'))
      return
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort)
      resolve()
    }, ms)
    const onAbort = () => {
      clearTimeout(timer)
      reject(signal?.reason ?? new Error('aborted'))
    }
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}
