import type { BootstrapData, FlowDocument } from '@/types/flow'

export type DocumentRole = 'owner' | 'editor' | 'commenter' | 'viewer' | 'none'

const rank: Record<DocumentRole, number> = { none: 0, viewer: 1, commenter: 2, editor: 3, owner: 4 }

function normalizeRole(value: string | undefined): DocumentRole {
  const role = (value ?? '').trim().toLowerCase()
  return role === 'owner' || role === 'editor' || role === 'commenter' || role === 'viewer' ? role : 'none'
}

/**
 * The viewer's role on a document, mirroring the server's documentRole:
 * workspace admins and the creator own it; an explicit permission list is an
 * ACL (best matching grant wins); otherwise team documents are viewable by
 * team members and unscoped documents by everyone. The server enforces the
 * same rules (HTTP and the collaboration socket); the UI uses this only to
 * choose between editing, commenting and reading.
 */
export function documentViewerRole(data: Pick<BootstrapData, 'viewer' | 'viewerRole' | 'workspace' | 'teamMembers'>, document: Pick<FlowDocument, 'creator' | 'permissions' | 'teamIds'>): DocumentRole {
  const viewerRole = String(data.viewerRole ?? '')
  if (viewerRole === 'admin' || viewerRole === 'owner' || document.creator.id === data.viewer.id) return 'owner'
  const memberOf = (teamId: string) => (data.teamMembers ?? []).some(member => member.userId === data.viewer.id && member.teamId === teamId)
  let best: DocumentRole = 'none'
  for (const permission of document.permissions ?? []) {
    const matched = permission.subjectType === 'user' ? permission.subjectId === data.viewer.id
      : permission.subjectType === 'workspace' ? !permission.subjectId || permission.subjectId === data.workspace.id || permission.subjectId === data.workspace.urlKey
        : permission.subjectType === 'team' ? memberOf(permission.subjectId) : false
    const role = normalizeRole(permission.role)
    if (matched && rank[role] > rank[best]) best = role
  }
  if (best !== 'none') return best
  // Team members can always read a team document; grants decide the rest.
  if ((document.permissions ?? []).length) return document.teamIds.some(memberOf) ? 'viewer' : 'none'
  if (!document.teamIds.length || document.teamIds.some(memberOf)) return 'viewer'
  return 'none'
}

export const canEditDocument = (role: DocumentRole) => rank[role] >= rank.editor
export const canCommentOnDocument = (role: DocumentRole) => rank[role] >= rank.commenter
