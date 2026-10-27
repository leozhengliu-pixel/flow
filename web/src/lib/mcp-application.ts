import type { AuthorizedApplication, AuthorizedApplicationMember, AuthorizedApplicationPermission } from '@/lib/api'
import type { OAuthAuthorization } from '@/types/flow'

/** Client ID of the one Settings › Applications entry that folds every MCP client together (api: mcpApplicationID). */
export const MCP_APPLICATION_ID = 'mcp'

/** Dynamic client registration (the MCP OAuth flow) mints client IDs with this prefix (api: dynamicOAuthClientPrefix). */
const DYNAMIC_CLIENT_PREFIX = 'flow_mcp_'

/** How recently an authorization must have been used for its permissions to read "Actively used" (api: mcpActiveUseWindow). */
export const MCP_ACTIVE_USE_WINDOW_MS = 30 * 24 * 60 * 60 * 1000

/** A member's user-actor grant to a dynamically registered MCP client, folded into the "MCP" entry. */
export function isMCPAuthorization(item: OAuthAuthorization, installedClientIds: ReadonlySet<string> = new Set()) {
  return !item.revokedAt && item.actor !== 'app' && item.clientId.startsWith(DYNAMIC_CLIENT_PREFIX) && !installedClientIds.has(item.clientId)
}

/** Whether the entry is the folded "MCP" one, or `clientId` (an old per-client link) one of its clients. */
export function matchesApplication(entry: AuthorizedApplication, clientId: string) {
  return entry.clientId === clientId || Boolean(entry.clientIds?.includes(clientId))
}

/**
 * The "MCP" entry built from authorizations (the member view, which has only the viewer's own from the bootstrap):
 * the same shape the server returns to admins.
 */
export function mcpApplication(authorizations: OAuthAuthorization[], developerUrl: string, now = Date.now()): AuthorizedApplication {
  const members = new Map<string, AuthorizedApplicationMember>()
  const permissions = new Map<string, AuthorizedApplicationPermission>()
  const clientIds: string[] = []
  for (const item of authorizations) {
    if (!clientIds.includes(item.clientId)) clientIds.push(item.clientId)
    const used = Boolean(item.lastUsedAt) && now - Date.parse(item.lastUsedAt!) <= MCP_ACTIVE_USE_WINDOW_MS
    for (const scope of item.scopes) {
      const permission = permissions.get(scope) ?? { scope, activelyUsed: false }
      permission.activelyUsed ||= used
      permissions.set(scope, permission)
    }
    const member = members.get(item.userId) ?? { userId: item.userId, authorizedAt: item.createdAt, scopes: [] }
    if (Date.parse(item.createdAt) < Date.parse(member.authorizedAt)) member.authorizedAt = item.createdAt
    if (item.lastUsedAt && (!member.lastUsedAt || Date.parse(item.lastUsedAt) > Date.parse(member.lastUsedAt))) member.lastUsedAt = item.lastUsedAt
    member.scopes = [...new Set([...member.scopes, ...item.scopes])]
    members.set(item.userId, member)
  }
  return {
    clientId: MCP_APPLICATION_ID, kind: 'mcp', name: 'MCP', developerName: 'Flow', developerUrl, authorizations, clientIds,
    members: [...members.values()].sort((a, b) => Date.parse(a.authorizedAt) - Date.parse(b.authorizedAt)),
    permissions: [...permissions.values()],
  }
}
