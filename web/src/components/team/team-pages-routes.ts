const TEAM_PAGE_KINDS = new Set([
  "team-overview",
  "team-documents",
  "team-loops",
  "team-members",
  "team-board",
  "team-triage",
  "team-updates",
  "team-update",
  "team-resources",
  "team-links",
  "team-archive",
  "team-cycles",
  "cycle",
  "cycle-upcoming",
  "team-initiatives",
  "team-issues",
  "team-saved-view",
  "team-views",
  "team-views-new",
  "team-projects",
  "team-projects-new-view",
  "team-projects-saved-view",
]);

export function isTeamPagesRoute(kind: string) {
  return TEAM_PAGE_KINDS.has(kind);
}
