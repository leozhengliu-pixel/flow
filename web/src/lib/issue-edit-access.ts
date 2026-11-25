import type { BootstrapData, Issue } from '@/types/flow'

/**
 * Whether the viewer may change an issue (edit its description, add, edit or remove its attachments and links). It
 * mirrors the server's rule, which still decides: a workspace admin or owner, the issue's creator, a member of its team
 * or someone it was explicitly shared with as editor or owner. Anyone else only reads and comments (a workspace member
 * outside the team of a public team's issue). When the data does not say who is in which team, the controls stay
 * available and the server refuses what is not allowed.
 */
export function viewerCanEditIssue(issue: Pick<Issue, 'creator' | 'team' | 'permissions'>, data: Pick<BootstrapData, 'viewer' | 'viewerRole' | 'teamMembers'>): boolean {
  const viewerId = data.viewer.id
  if (data.viewerRole === 'admin' || data.viewerRole === 'owner') return true
  if (issue.creator?.id === viewerId) return true
  const teams = data.teamMembers ?? []
  if (!teams.length) return true
  if (teams.some(member => member.teamId === issue.team.id && member.userId === viewerId)) return true
  const viewerTeams = new Set(teams.filter(member => member.userId === viewerId).map(member => member.teamId))
  return (issue.permissions ?? []).some(grant => {
    const role = (grant.role ?? '').toLowerCase()
    if (role !== 'editor' && role !== 'owner') return false
    return grant.subjectType === 'workspace' || (grant.subjectType === 'user' && grant.subjectId === viewerId) || (grant.subjectType === 'team' && viewerTeams.has(grant.subjectId))
  })
}
