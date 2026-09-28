import { useContext } from 'react'
import { EntityStoreContext } from './application-store-context'
import type { WorkspaceStore } from './workspace-store'

/**
 * Root synced workspace store hook (LS-0770).
 * Linear: `useStore` → ApplicationStoreContext entity root.
 * Flow: `useWorkspaceStore` with `getById` + reserved `hydrateModel(type, id)`.
 */
export function useWorkspaceStore(): WorkspaceStore {
  const store = useContext(EntityStoreContext)
  if (!store) {
    throw new Error('useWorkspaceStore must be used within WorkspaceStoreProvider')
  }
  return store
}

/** Linear-compatible alias for useWorkspaceStore (LS-0770). */
export const useStore = useWorkspaceStore
