import { useMemo, type ReactNode } from 'react'
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
  const entityStore = useMemo(() => storeProp ?? createWorkspaceStore(data), [data, storeProp])
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
