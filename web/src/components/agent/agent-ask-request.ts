/** Window event that asks the Agent page to send a question in its own chat (⌘K "Ask Flow" while on the Agent page). */
export const AGENT_PAGE_ASK_EVENT = 'flow-agent-page-ask'

export type AgentPageAskDetail = { prompt: string }
