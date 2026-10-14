import type { BootstrapData, Team } from "@/types/flow";

type TeamAccessData = Pick<BootstrapData, "teams" | "teamMembers" | "teamSettings" | "viewer" | "viewerRole">;

export function isWorkspaceAdmin(data: Pick<BootstrapData, "viewerRole">) {
  return data.viewerRole === "admin" || data.viewerRole === "owner";
}

/** Why the viewer cannot leave the team (the reference app's copy), or undefined when they can. */
export function teamLeaveDisabledReason(data: BootstrapData, team: Team) {
  const membership = data.teamMembers.find(item => item.teamId === team.id && item.userId === data.viewer.id)
  if (!membership) return 'You are not a member of this team'
  if (membership.managedSource) return 'Managed by SCIM'
  if (membership.role === 'owner' && !data.teamMembers.some(item => item.teamId === team.id && item.userId !== data.viewer.id && item.role === 'owner')) return 'A team needs at least one owner'
  const active = new Set(data.teams.filter(item => !item.retiredAt).map(item => item.id))
  const memberships = data.teamMembers.filter(item => item.userId === data.viewer.id && active.has(item.teamId))
  if (memberships.length <= 1) return 'You should be a member of at least one active team'
  if (data.viewerRole !== 'guest' && memberships.some(item => {
    const seen = new Set<string>()
    for (let parent = data.teamSettings[item.teamId]?.parentTeamId ?? data.teamParents?.[item.teamId]; parent && !seen.has(parent); parent = data.teamSettings[parent]?.parentTeamId ?? data.teamParents?.[parent]) {
      if (parent === team.id) return true
      seen.add(parent)
    }
    return false
  })) return 'Leave the sub-teams before leaving this team'
  return undefined
}

/** A private team, or a top-level team restricted to its members. */
export function isPrivateTeamBoundary(data: Pick<BootstrapData, "teamSettings">, team: Team) {
  const settings = data.teamSettings?.[team.id];
  return Boolean(team.private) || settings?.access === "private" || (settings?.access === "restricted" && !settings.parentTeamId);
}

/**
 * Whether the viewer may read a team's content (issues, projects, documents,
 * views). As in the reference app, a workspace admin sees every team and can manage its
 * settings, but a private team's content needs membership — of the team, or of
 * the private parent team it sits in. Non-admins only receive teams they can read.
 * Mirrors the server's store.TeamContentVisible.
 */
export function teamContentAccessible(data: TeamAccessData, team: Team) {
  const memberOf = (teamId: string) =>
    data.teamMembers.some((member) => member.teamId === teamId && member.userId === data.viewer.id);
  if (memberOf(team.id)) return true;
  if (isPrivateTeamBoundary(data, team)) return false;
  const seen = new Set([team.id]);
  for (let parentId = data.teamSettings?.[team.id]?.parentTeamId; parentId && !seen.has(parentId); parentId = data.teamSettings?.[parentId]?.parentTeamId) {
    seen.add(parentId);
    const parent = data.teams.find((item) => item.id === parentId);
    if (parent && isPrivateTeamBoundary(data, parent)) return memberOf(parentId);
  }
  return true;
}
