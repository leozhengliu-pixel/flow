import { createContext } from 'react'
import type { AccountBootstrap, AuthSession, BootstrapData, UserSettings, WorkspaceSettings } from '@/types/flow'
import type { WorkspaceStore } from './workspace-store'

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
