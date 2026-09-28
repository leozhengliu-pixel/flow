import { createContext, useContext } from 'react'
import { issuePath, projectPath } from '@/lib/app-routes'
import { ApplicationStoreContext } from '@/store/application-store-context'
import type { BootstrapData } from '@/types/flow'
import type { AgentEntityRef } from './agent-answer-content'

/** Workspace data the inline entity chips resolve against. */
export const AgentEntityDataContext = createContext<BootstrapData | undefined>(undefined)

/** The surface's own data, or the app's bootstrap data when the surface was not handed any. */
export function useAgentEntityData(explicit?: BootstrapData) {
  const store = useContext(ApplicationStoreContext)
  return explicit ?? store?.data ?? undefined
}

export function agentEntityHref(data: BootstrapData, entity: AgentEntityRef) {
  return entity.kind === 'issue' ? issuePath(data.workspace.urlKey, entity.issue) : projectPath(data.workspace.urlKey, entity.project)
}
