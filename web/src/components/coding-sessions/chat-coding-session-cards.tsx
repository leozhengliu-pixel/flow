import type { AgentMessage, BootstrapData } from '@/types/flow'
import { CodingSessionCard } from './coding-session-card'
import { codingSessionIdsFromMessage } from './coding-session-model'
import { useDismissedCodingSessions } from './use-coding-sessions'

/**
 * Agent chat: asking for code changes makes the agent call `start_coding_session` (track L); the reply then shows a
 * live card for each session it started.
 */
export function ChatCodingSessionCards({ message, data }: { message: Pick<AgentMessage, 'parts'>; data?: BootstrapData }) {
  const ids = data ? codingSessionIdsFromMessage(message) : []
  if (!data || !ids.length) return null
  return <ChatCards ids={ids} data={data}/>
}

function ChatCards({ ids, data }: { ids: string[]; data: BootstrapData }) {
  const { dismissed, dismiss } = useDismissedCodingSessions(data.workspace.urlKey, data.viewer.id)
  const shown = ids.filter(id => !dismissed.has(id))
  if (!shown.length) return null
  return <div className="coding-session-chat-cards">{shown.map(id => <CodingSessionCard key={id} sessionId={id} data={data} onDismiss={() => dismiss(id)}/>)}</div>
}
