import type { BootstrapData } from "@/types/flow";

/** Same limit as workspace guidance (the API rejects longer team guidance). */
export const TEAM_AGENT_GUIDANCE_LIMIT = 8000;

type InheritedGuidance = { key: string; source: string; teamName?: string; body: string };

/** Guidance a team inherits, applied before its own: the workspace's, then parent teams from the root down. */
export function inheritedTeamAgentGuidance(data: BootstrapData, teamId: string): InheritedGuidance[] {
  const ancestors: string[] = [];
  const seen = new Set<string>([teamId]);
  for (let id = data.teamSettings?.[teamId]?.parentTeamId; id && !seen.has(id); id = data.teamSettings?.[id]?.parentTeamId) {
    seen.add(id);
    ancestors.unshift(id);
  }
  const result: InheritedGuidance[] = [];
  const workspace = data.workspaceSettings?.agentInstructions?.trim();
  if (workspace) result.push({ key: "workspace", source: "Workspace", body: workspace });
  for (const id of ancestors) {
    const body = data.teamSettings?.[id]?.agentGuidance?.trim();
    if (body) result.push({ key: id, source: "Parent team", teamName: data.teams.find((team) => team.id === id)?.name ?? id, body });
  }
  return result;
}
