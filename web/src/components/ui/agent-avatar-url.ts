/** The built-in "Flow" agent member's avatar url (server `EnsureBuiltinApplication`). */
export const BUILTIN_AGENT_AVATAR_URL = '/favicon.svg'

export function isBuiltinAgentAvatarUrl(url?: string | null) {
  return url === BUILTIN_AGENT_AVATAR_URL
}
