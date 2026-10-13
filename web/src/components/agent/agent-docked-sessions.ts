/**
 * Chats that live in a page's agent sidebar (an issue, project, initiative or document) are saved with the
 * "toolbar" location, like the floating chat. The app reopens an unclosed toolbar chat as the floating panel, so a
 * docked chat is marked closed for it: otherwise sending in the sidebar also opened the same chat floating on top,
 * and it kept popping up on other pages after a reload.
 */
export const CLOSED_AGENT_SESSIONS_KEY = 'flow:closed-agent-sessions'
export const AGENT_SESSION_DOCKED_EVENT = 'flow:agent-session-docked'

export function readClosedAgentSessionIds(): Set<string> {
  try {
    return new Set<string>(JSON.parse(localStorage.getItem(CLOSED_AGENT_SESSIONS_KEY) ?? '[]'))
  } catch {
    return new Set<string>()
  }
}

export function markAgentSessionDocked(id: string) {
  if (!id) return
  const closed = readClosedAgentSessionIds()
  if (!closed.has(id)) {
    closed.add(id)
    try { localStorage.setItem(CLOSED_AGENT_SESSIONS_KEY, JSON.stringify([...closed].slice(-200))) } catch { /* storage is best-effort */ }
  }
  window.dispatchEvent(new CustomEvent<{ id: string }>(AGENT_SESSION_DOCKED_EVENT, { detail: { id } }))
}
