import { agentDraftStorageKey, clearAgentDraft } from './agent-drafts'

export function clearEntityThreadDraft(conversationDraftKey: string) {
  clearAgentDraft(agentDraftStorageKey(conversationDraftKey))
}
