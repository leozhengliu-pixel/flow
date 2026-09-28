import { useContext } from 'react'
import {
  ApplicationStoreContext,
  type ApplicationStoreValue,
} from './application-store-context'

/**
 * Application shell store (LS-0724): bootstrap snapshot, session, account,
 * and viewer/workspace UI prefs — without deep prop drilling from App.
 */
export function useApplicationStore(): ApplicationStoreValue {
  const value = useContext(ApplicationStoreContext)
  if (!value) {
    throw new Error('useApplicationStore must be used within WorkspaceStoreProvider')
  }
  return value
}
