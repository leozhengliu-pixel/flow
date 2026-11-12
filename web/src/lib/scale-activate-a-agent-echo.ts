import type { RealtimeEvent } from '@/types/flow'

/** The initiating page already owns its new chat; its echo must not open another panel. */
export function locallyCreatedAgentSession(event: Pick<RealtimeEvent, 'type' | 'aggregateId' | 'clientId'>, clientId: string): string | undefined {
  return clientId && event.clientId === clientId && event.type === 'agent.session_created' ? event.aggregateId : undefined
}
