import { useMemo, useState, type ReactNode } from 'react'
import type { AccountBootstrap, AuthSession, BootstrapData } from '@/types/flow'
import { ApplicationStoreContext, EntityStoreContext, type ApplicationStoreValue } from './application-store-contexts'
import { createWorkspaceStore, type WorkspaceStore } from './workspace-store'

export type WorkspaceStoreProviderProps = {
  data: BootstrapData | null
  session: AuthSession | null
  account: AccountBootstrap | null
  children: ReactNode
  /** Optional prebuilt store (tests). Defaults to createWorkspaceStore(data). */
  store?: WorkspaceStore
}

export function WorkspaceStoreProvider({
  data,
  session,
  account,
  children,
  store: storeProp,
}: WorkspaceStoreProviderProps) {
  const [cached, setCached] = useState(() => ({ data, store: storeProp ?? createWorkspaceStore(data) }))
  let entityStore = storeProp ?? cached.store
  if (cached.data !== data || storeProp && cached.store !== storeProp) {
    entityStore = storeProp ?? createWorkspaceStore(data, cached.store)
    setCached({ data, store: entityStore })
  }
  const applicationStore = useMemo<ApplicationStoreValue>(
    () => ({
      data,
      session,
      account,
      userSettings: data ? data.userSettings?.[data.viewer.id] : undefined,
      workspaceSettings: data?.workspaceSettings,
    }),
    [data, session, account],
  )

  return (
    <ApplicationStoreContext.Provider value={applicationStore}>
      <EntityStoreContext.Provider value={entityStore}>{children}</EntityStoreContext.Provider>
    </ApplicationStoreContext.Provider>
  )
}
