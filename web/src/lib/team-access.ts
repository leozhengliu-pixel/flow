import type { BootstrapData, Team } from "@/types/flow";

type TeamAccessData = Pick<BootstrapData, "teams" | "teamMembers" | "teamSettings" | "viewer" | "viewerRole">;

export function isWorkspaceAdmin(data: Pick<BootstrapData, "viewerRole">) {
  return data.viewerRole === "admin" || data.viewerRole === "owner";
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
