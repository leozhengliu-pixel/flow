import { useSyncExternalStore } from 'react'

/**
 * What issue-list group headers count: the group's estimate total beside the points icon, or plain issue counts.
 * One device-wide view option shared by team lists and My issues, kept across reloads (as the reference does).
 */
export type ListGroupCountMode = 'points' | 'issues'

const STORAGE_KEY = 'flow:list-group-count'
const listeners = new Set<() => void>()
let mode: ListGroupCountMode | undefined

function read(): ListGroupCountMode {
  if (mode) return mode
  try { mode = localStorage.getItem(STORAGE_KEY) === 'issues' ? 'issues' : 'points' } catch { mode = 'points' }
  return mode
}

export function setListGroupCountMode(next: ListGroupCountMode) {
  mode = next
  try { localStorage.setItem(STORAGE_KEY, next) } catch { /* storage unavailable: the choice lasts for this page */ }
  listeners.forEach(listener => listener())
}

/** Test hook: forget the cached choice so the next read goes back to storage. */
export function resetListGroupCountMode() {
  mode = undefined
  listeners.forEach(listener => listener())
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  const storage = (event: StorageEvent) => { if (event.key === STORAGE_KEY) { mode = undefined; listener() } }
  window.addEventListener('storage', storage)
  return () => { listeners.delete(listener); window.removeEventListener('storage', storage) }
}

export function useListGroupCountMode(): [ListGroupCountMode, (next: ListGroupCountMode) => void] {
  return [useSyncExternalStore(subscribe, read, () => 'points'), setListGroupCountMode]
}
