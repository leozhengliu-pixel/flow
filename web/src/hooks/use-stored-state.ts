import { useCallback, useEffect, useState } from 'react'
import { ClientStorage, type StorageMechanism } from '@/lib/client-storage'

const DEFAULT_TTL_MS = 5 * 60 * 1000

type StoredTTLEnvelope<T> = {
  value: T
  timestamp: number
}

/**
 * Persist React state through ClientStorage (LS-0771).
 * `local` mechanism also mirrors cross-tab via the `storage` event.
 */
export function useStoredState<T>(
  key: string,
  defaultValue: T,
  mechanism: StorageMechanism = 'session',
): [T, (next: T) => void] {
  const [value, setValue] = useState<T>(
    () => ClientStorage.get<T>(key, { storageMechanism: mechanism }) ?? defaultValue,
  )

  const setStored = useCallback(
    (next: T) => {
      setValue(next)
      if (Object.is(next, defaultValue)) {
        ClientStorage.remove(key, mechanism)
      } else {
        ClientStorage.set(key, next, mechanism)
      }
    },
    [defaultValue, key, mechanism],
  )

  useEffect(() => {
    if (mechanism !== 'local') return
    const onStorage = (event: StorageEvent) => {
      if (event.key !== key) return
      if (event.newValue === null) {
        setValue(defaultValue)
        return
      }
      if (!event.newValue) return
      try {
        setValue(ClientStorage.parse<T>(event.newValue))
      } catch {
        /* Ignore malformed cross-tab payloads. */
      }
    }
    window.addEventListener('storage', onStorage)
    return () => window.removeEventListener('storage', onStorage)
  }, [defaultValue, key, mechanism])

  return [value, setStored]
}

/**
 * Session-backed state with TTL (default 5 minutes). Refreshes timestamp on unmount.
 */
export function useStoredStateWithTTL<T>(
  key: string,
  defaultValue: T,
  ttlMs: number = DEFAULT_TTL_MS,
): [T, (next: T) => void] {
  const [value, setValue] = useState<T>(() => {
    const envelope = ClientStorage.get<StoredTTLEnvelope<T>>(key, { storageMechanism: 'session' })
    if (!envelope) return defaultValue
    if (Date.now() - envelope.timestamp >= ttlMs) {
      ClientStorage.remove(key, 'session')
      return defaultValue
    }
    return envelope.value
  })

  const setStored = useCallback(
    (next: T) => {
      setValue(next)
      if (Object.is(next, defaultValue)) {
        ClientStorage.remove(key, 'session')
      } else {
        ClientStorage.setSession(key, { value: next, timestamp: Date.now() } satisfies StoredTTLEnvelope<T>)
      }
    },
    [defaultValue, key],
  )

  useEffect(
    () => () => {
      const envelope = ClientStorage.get<StoredTTLEnvelope<T>>(key, { storageMechanism: 'session' })
      if (!envelope) return
      ClientStorage.setSession(key, { ...envelope, timestamp: Date.now() })
    },
    [key],
  )

  return [value, setStored]
}
