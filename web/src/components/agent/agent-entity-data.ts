import { createContext, useContext } from 'react'
import { ApplicationStoreContext } from '@/store/application-store-context'
import type { BootstrapData } from '@/types/flow'
import { agentEntityPath, type AgentEntity } from './agent-entity-refs'

/** Workspace data the inline entity chips resolve against. */
export const AgentEntityDataContext = createContext<BootstrapData | undefined>(undefined)

/** The surface's own data, the data its nearest `AgentEntityDataContext` provides (an editor handed its own workspace data), or the app's bootstrap data. */
export function useAgentEntityData(explicit?: BootstrapData) {
  const store = useContext(ApplicationStoreContext)
  const provided = useContext(AgentEntityDataContext)
  return explicit ?? provided ?? store?.data ?? undefined
}

export function agentEntityHref(data: BootstrapData, entity: AgentEntity) {
  return agentEntityPath(data, entity)
}
