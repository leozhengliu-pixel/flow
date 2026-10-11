import { labelTeamScopeIds } from '@/lib/labels'
import type { BootstrapData, IssueTemplate } from '@/types/flow'

/**
 * Issue templates come in two kinds:
 * - workspace templates (Settings › Issues › Templates) apply to every team; their `teamId` is only the default team
 *   property the template sets on the new issue;
 * - team templates (team Settings › Templates) belong to `teamId` and are inherited by its sub-teams.
 * A workspace template whose visibility was narrowed to one team (`visibilityTeamId`) behaves like that team's template.
 */
export function isWorkspaceIssueTemplate(template: IssueTemplate) {
  return template.scope === 'workspace' && !template.visibilityTeamId
}

/** The team a team template belongs to, or undefined for a workspace template. */
export function issueTemplateOwnerTeamId(template: IssueTemplate) {
  if (template.visibilityTeamId) return template.visibilityTeamId
  return template.scope === 'workspace' ? undefined : template.teamId || undefined
}

/** Workspace templates, newest first as the API returns them. */
export function workspaceIssueTemplates(templates: IssueTemplate[]) {
  return templates.filter(isWorkspaceIssueTemplate)
}

/** Templates owned by one of `teamIds` (a team and, for inheritance, its parent teams). */
export function teamOwnedIssueTemplates(templates: IssueTemplate[], teamIds: Iterable<string>) {
  const ids = new Set(teamIds)
  return templates.filter(template => {
    const owner = issueTemplateOwnerTeamId(template)
    return Boolean(owner && ids.has(owner))
  })
}

/** Every template a new issue in `teamId` can start from: the team's (and its parents') templates, then the workspace's. */
export function issueTemplatesForTeam(data: Pick<BootstrapData, 'issueTemplates' | 'teams' | 'teamSettings'>, teamId: string) {
  const scopes = labelTeamScopeIds(teamId, data.teams, data.teamSettings)
  return [...teamOwnedIssueTemplates(data.issueTemplates, scopes), ...workspaceIssueTemplates(data.issueTemplates)]
}

/** A link that opens the new-issue composer with the template applied. */
export function issueTemplateCreateUrl(data: Pick<BootstrapData, "workspace">, template: IssueTemplate) {
  return `${location.origin}/${encodeURIComponent(data.workspace.urlKey)}/issues/all?create=1&template=${encodeURIComponent(template.id)}`;
}

/** "today", "3 days ago", … for a template's creation time. */
export function relativeTemplateTime(value: string, t: (value: string) => string) {
  const created = new Date(value).getTime();
  if (!Number.isFinite(created)) return "";
  const days = Math.floor((Date.now() - created) / 86_400_000);
  if (days < 1) return t("today");
  if (days === 1) return t("yesterday");
  if (days < 30) return t("{count} days ago").replace("{count}", String(days));
  const months = Math.floor(days / 30);
  if (months < 12) return t(months === 1 ? "1 month ago" : "{count} months ago").replace("{count}", String(months));
  const years = Math.floor(months / 12);
  return t(years === 1 ? "1 year ago" : "{count} years ago").replace("{count}", String(years));
}
