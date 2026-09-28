/**
 * Unified browser storage facade (LS-0109 ClientStorage).
 * localStorage / sessionStorage only — IDB stays in realtime-cache.
 */

export type StorageMechanism = 'local' | 'session'

export type ClientStorageGetOptions = {
  storageMechanism?: StorageMechanism
  /** When false, parse/load errors are silent (default true). */
  logError?: boolean
}

export type PersistentUser = { id: string }

/** Keys retained across logout (parity with Linear ClientStorage.clearAllNonAuthData). */
export const CLIENT_STORAGE_AUTH_LOCAL_KEYS = [
  'clientId',
  'ApplicationStore',
  'featureFlagMetadata',
  'FeatureFlags',
  'isLinearSuperuser',
] as const

export const CLIENT_STORAGE_AUTH_SESSION_KEYS = ['ApplicationStore'] as const

const EPHEMERAL_PREFIX = 'ephemeral_'

function encodeValue(_key: string, value: unknown): unknown {
  if (value instanceof Map) {
    return { __type__: 'Map', value: Array.from(value.entries()) }
  }
  if (value instanceof Set) {
    return { __type__: 'Set', value: Array.from(value.values()) }
  }
  return value
}

function decodeValue(_key: string, value: unknown): unknown {
  if (typeof value === 'object' && value && '__type__' in value) {
    const typed = value as { __type__?: string; value?: unknown }
    try {
      if (typed.__type__ === 'Map' && Array.isArray(typed.value)) {
        return new Map(typed.value as [unknown, unknown][])
      }
      if (typed.__type__ === 'Set' && Array.isArray(typed.value)) {
        return new Set(typed.value as unknown[])
      }
    } catch {
      return undefined
    }
  }
  return value
}

function getBrowserStorage(mechanism: StorageMechanism): Storage | undefined {
  try {
    if (mechanism === 'local') {
      if (typeof localStorage !== 'undefined') return localStorage
      return undefined
    }
    if (typeof sessionStorage !== 'undefined') return sessionStorage
    return undefined
  } catch {
    return undefined
  }
}

function isQuotaExceeded(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'QuotaExceededError'
}

function clearEphemeralSessionKeys() {
  const storage = getBrowserStorage('session')
  if (!storage) return
  for (const key of ClientStorage.getKeys('session')) {
    if (key.startsWith(EPHEMERAL_PREFIX)) storage.removeItem(key)
  }
}

export class ClientStorage {
  static stringify(value: unknown): string {
    return JSON.stringify(value, encodeValue)
  }

  static parse<T = unknown>(raw: string): T {
    return JSON.parse(raw, decodeValue) as T
  }

  static getStorage(mechanism: StorageMechanism = 'local'): Storage | undefined {
    return getBrowserStorage(mechanism)
  }

  static getKeys(mechanism: StorageMechanism = 'local'): string[] {
    const storage = getBrowserStorage(mechanism)
    if (!storage) return []
    const keys: string[] = []
    for (let index = 0; index < storage.length; index += 1) {
      const key = storage.key(index)
      if (key) keys.push(key)
    }
    return keys
  }

  static getString(key: string, mechanism: StorageMechanism = 'local'): string | undefined {
    const storage = getBrowserStorage(mechanism)
    if (!storage) return undefined
    try {
      return storage.getItem(key) || undefined
    } catch {
      return undefined
    }
  }

  static setString(
    key: string,
    value: string,
    mechanism: StorageMechanism = 'local',
    onError?: (error: unknown) => void,
  ): boolean {
    const storage = getBrowserStorage(mechanism)
    if (!storage) return false
    try {
      storage.setItem(key, value)
      return true
    } catch (error) {
      onError?.(error)
      return false
    }
  }

  static set(key: string, value: unknown, mechanism: StorageMechanism = 'local'): boolean {
    try {
      if (mechanism === 'session') return this.setSession(key, value)
      return this.setString(key, this.stringify(value), mechanism)
    } catch {
      return false
    }
  }

  static setSession(key: string, value: unknown): boolean {
    try {
      const raw = this.stringify(value)
      const first = this.setString(key, raw, 'session', (error) => {
        if (!isQuotaExceeded(error)) return
        clearEphemeralSessionKeys()
      })
      if (first) return true
      const second = this.setString(key, raw, 'session', (error) => {
        if (!isQuotaExceeded(error)) return
        this.clear('session')
      })
      if (second) return true
      return this.setString(key, raw, 'session')
    } catch {
      return false
    }
  }

  static setEphemeralSession(key: string, value: unknown): boolean {
    return this.setSession(`${EPHEMERAL_PREFIX}${key}`, value)
  }

  static setPersistent(key: string, user: PersistentUser, value: unknown): boolean {
    try {
      return this.setString(`p_${user.id}_${key}`, this.stringify(value), 'local')
    } catch {
      return false
    }
  }

  static get<T = unknown>(key: string, options?: ClientStorageGetOptions): T | undefined {
    const mechanism = options?.storageMechanism ?? 'local'
    try {
      const raw = this.getString(key, mechanism)
      if (raw === undefined) return undefined
      return this.parse<T>(raw)
    } catch {
      return undefined
    }
  }

  static getPersistent<T = unknown>(key: string, user: PersistentUser): T | undefined {
    try {
      const raw = this.getString(`p_${user.id}_${key}`)
      if (raw === undefined) return undefined
      return this.parse<T>(raw)
    } catch {
      return undefined
    }
  }

  static getSession<T = unknown>(key: string): T | undefined {
    return this.get<T>(key, { storageMechanism: 'session' })
  }

  static getEphemeralSession<T = unknown>(key: string): T | undefined {
    return this.getSession<T>(`${EPHEMERAL_PREFIX}${key}`)
  }

  static remove(key: string, mechanism: StorageMechanism = 'local'): boolean {
    const storage = getBrowserStorage(mechanism)
    if (!storage) return false
    try {
      storage.removeItem(key)
      return true
    } catch {
      return false
    }
  }

  static removeSession(key: string): boolean {
    return this.remove(key, 'session')
  }

  static removeEphemeralSession(key: string): boolean {
    return this.remove(`${EPHEMERAL_PREFIX}${key}`, 'session')
  }

  static removePersistent(key: string, user: PersistentUser): boolean {
    return this.remove(`p_${user.id}_${key}`)
  }

  static removeAllWithCondition(
    predicate: (key: string) => boolean,
    mechanism: StorageMechanism = 'local',
  ): boolean {
    const storage = getBrowserStorage(mechanism)
    if (!storage) return false
    for (const key of this.getKeys(mechanism)) {
      if (predicate(key)) storage.removeItem(key)
    }
    return true
  }

  /** Clears non-user-scoped keys (`p_*` retained). */
  static clear(mechanism: StorageMechanism = 'local'): boolean {
    return this.removeAllWithCondition((key) => !key.startsWith('p_'), mechanism)
  }

  /**
   * Logout cleanup: drop prefs/drafts while keeping Linear-parity auth shell keys.
   * User-scoped `p_{userId}_*` keys are also cleared (they are non-auth prefs).
   */
  static clearAllNonAuthData(): void {
    const keepLocal = new Set<string>(CLIENT_STORAGE_AUTH_LOCAL_KEYS)
    const keepSession = new Set<string>(CLIENT_STORAGE_AUTH_SESSION_KEYS)
    this.removeAllWithCondition((key) => !keepLocal.has(key), 'local')
    this.removeAllWithCondition((key) => !keepSession.has(key), 'session')
  }

  static persistentKey(userId: string, key: string): string {
    return `p_${userId}_${key}`
  }
}
