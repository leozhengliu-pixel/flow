import { useSyncExternalStore } from 'react'

export interface CodeBlockSettings { wrap: boolean; lineNumbers: boolean }

const STORAGE_KEY = 'flow.editor.code-block'
const DEFAULTS: CodeBlockSettings = { wrap: false, lineNumbers: true }
const listeners = new Set<() => void>()
let current: CodeBlockSettings | undefined

function read(): CodeBlockSettings {
  try {
    const stored = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? 'null') as Partial<CodeBlockSettings> | null
    return {
      wrap: typeof stored?.wrap === 'boolean' ? stored.wrap : DEFAULTS.wrap,
      lineNumbers: typeof stored?.lineNumbers === 'boolean' ? stored.lineNumbers : DEFAULTS.lineNumbers,
    }
  } catch {
    return DEFAULTS
  }
}

function snapshot() {
  current ??= read()
  return current
}

/** Stores the viewer's code block display choices (per browser, shared by every code block); storage failures are ignored. */
export function updateCodeBlockSettings(patch: Partial<CodeBlockSettings>) {
  current = { ...snapshot(), ...patch }
  try { window.localStorage.setItem(STORAGE_KEY, JSON.stringify(current)) } catch { /* private window or blocked storage: keep it in memory */ }
  for (const listener of listeners) listener()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  const onStorage = (event: StorageEvent) => {
    if (event.key !== STORAGE_KEY && event.key !== null) return
    current = read()
    listener()
  }
  window.addEventListener('storage', onStorage)
  return () => { listeners.delete(listener); window.removeEventListener('storage', onStorage) }
}

export function useCodeBlockSettings() {
  return useSyncExternalStore(subscribe, snapshot, () => DEFAULTS)
}

/** Test hook: forget the cached settings so the next read goes back to storage. */
export function resetCodeBlockSettingsCache() {
  current = undefined
}
