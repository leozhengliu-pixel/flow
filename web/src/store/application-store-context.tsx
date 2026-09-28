import { createContext, useMemo, type ReactNode } from 'react'
import type { AccountBootstrap, AuthSession, BootstrapData, UserSettings, WorkspaceSettings } from '@/types/flow'
import { createWorkspaceStore, type WorkspaceStore } from './workspace-store'

/**
 * Dual contexts mirroring Linear ApplicationStoreContext (LS-0063):
 * - EntityStoreContext → useWorkspaceStore / useStore (synced entity root)
 * - ApplicationStoreContext → useApplicationStore (bootstrap / session / UI prefs shell)
 */
export type ApplicationStoreValue = {
  data: BootstrapData | null
  session: AuthSession | null
  account: AccountBootstrap | null
  userSettings: UserSettings | undefined
  workspaceSettings: WorkspaceSettings | undefined
}

export const EntityStoreContext = createContext<WorkspaceStore | null>(null)
export const ApplicationStoreContext = createContext<ApplicationStoreValue | null>(null)

/** @deprecated Alias kept for Linear naming parity (LS-0063). Prefer EntityStoreContext. */
export const WorkspaceStoreContext = EntityStoreContext

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
