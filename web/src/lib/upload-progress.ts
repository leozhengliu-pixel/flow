/** The share (0 to 1) of each file still being uploaded that has been sent, keyed by the file's placeholder URL. */
const fractions = new Map<string, number>()
const listeners = new Map<string, Set<() => void>>()

export function reportUploadProgress(key: string, fraction: number) {
  fractions.set(key, Math.max(0, Math.min(1, fraction)))
  listeners.get(key)?.forEach(listener => listener())
}

export function clearUploadProgress(key: string) {
  fractions.delete(key)
  listeners.get(key)?.forEach(listener => listener())
}

export function readUploadProgress(key: string) {
  return fractions.get(key) ?? 0
}

export function subscribeUploadProgress(key: string, listener: () => void) {
  const set = listeners.get(key) ?? new Set<() => void>()
  set.add(listener)
  listeners.set(key, set)
  return () => { set.delete(listener); if (!set.size) listeners.delete(key) }
}
