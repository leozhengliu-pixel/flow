/**
 * LS-0781 useViewPreferences — hydrate org-scoped view preferences by type.
 */
import { useEffect, useMemo, useSyncExternalStore } from 'react'
import {
  ViewPreferencesHandle,
  ViewPreferencesOrganization,
  type ViewPreferenceType,
  type ViewPreferencesRecord,
} from '@/lib/view-preferences'

export type UseViewPreferencesResult = {
  preferences: ViewPreferencesRecord
  setPreference: ViewPreferencesHandle['setPreference']
  save: ViewPreferencesHandle['save']
  hydrate: () => ViewPreferencesRecord
  handle: ViewPreferencesHandle
}

export function getViewPreferences(orgKey: string, type: ViewPreferenceType): ViewPreferencesHandle {
  return new ViewPreferencesOrganization(orgKey).getViewPreferences(type)
}

export function useViewPreferences(
  type: ViewPreferenceType,
  orgKey = 'default',
): UseViewPreferencesResult {
  const handle = useMemo(() => getViewPreferences(orgKey, type), [orgKey, type])

  useEffect(() => {
    handle.hydrate()
  }, [handle])

  const preferences = useSyncExternalStore(
    listener => handle.subscribe(listener),
    () => handle.get(),
    () => handle.get(),
  )

  return {
    preferences,
    setPreference: handle.setPreference.bind(handle),
    save: handle.save.bind(handle),
    hydrate: () => handle.hydrate(),
    handle,
  }
}

export function useOrganizationViewPreferences(orgKey: string) {
  const org = useMemo(() => new ViewPreferencesOrganization(orgKey), [orgKey])
  useEffect(() => {
    org.hydrate()
  }, [org])
  return org
}
