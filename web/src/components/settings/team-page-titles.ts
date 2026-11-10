import type { TeamSettingsSection } from "@/lib/app-routes";

/** Docs links of the team settings sub-pages (the reference shows a quiet "Docs ↗" after the lead sentence). */
export const TEAM_PAGE_DOCS: Partial<Record<TeamSettingsSection, string>> = {
  hierarchy: "https://flow.app/docs/team-hierarchy",
  estimates: "https://flow.app/docs/estimates",
  triage: "https://flow.app/docs/triage",
  cycles: "https://flow.app/docs/cycles",
  agents: "https://flow.app/docs/agents",
};

/** Page titles of the sub-pages (the overview row may use a shorter label). */
export const TEAM_PAGE_TITLES: Partial<Record<TeamSettingsSection, string>> = {
  security: "Access and permissions",
  workflow: "Automations",
  agents: "Team agents",
  hierarchy: "Team hierarchy",
  estimates: "Estimates",
  triage: "Triage",
  cycles: "Cycles",
  "agent-skills": "Agent skills",
  "agent-connectors": "Agent connectors",
  "ai-updates": "Project updates",
  "ai-summaries": "Resolved thread summaries",
};

/** Lead sentences under the page title. */
export const TEAM_PAGE_LEADS: Partial<Record<TeamSettingsSection, string>> = {
  hierarchy: "Teams can be nested to reflect your team structure and to share workflows and settings.",
  estimates: "Estimate issue complexity and plan your team’s capacity.",
  workflow: "Create issues by email and automate status updates, closing, and archiving.",
  triage:
    "Triage is a special inbox for managing issues created by integrations and members outside of this team. You’ll have a chance to review, accept or reject, and prioritize issues before they’re added to your team’s workflow.",
  cycles:
    "Cycles create rhythm and focus with short, time-boxed planning windows. Automations can create future cycles, carry over unfinished work, and move issues in or out based on status.",
  agents: "AI agents can work alongside you as teammates. Tackle complex tasks together or delegate entire issues end-to-end.",
  "agent-skills": "Add reusable instructions team members can use with Flow Agent",
  "agent-connectors": "Add MCP connectors that team members can use with Flow Agent",
  "ai-updates": "Customize how project updates should be written",
  "ai-summaries": "Automatically generate summaries for resolved threads",
};

