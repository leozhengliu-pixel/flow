import type { BootstrapData, WorkspaceSettings } from '@/types/flow'
import type { SettingsPageId, TeamSettingsSection } from './app-routes'
import { canManageTeamSettings } from './settings-permissions'

/**
 * How the viewer may use a settings page:
 * - `hidden`: not listed in the settings navigation and blocked when opened by URL.
 * - `read`: listed and viewable, but every control is read-only (with a notice).
 * - `edit`: listed and editable. Pages that mix workspace-admin and personal
 *   controls (AI, Loops, Pulse, Asks, Releases, Integrations) gate their own
 *   controls and are reported as `edit`.
 *
 * Mirrors Linear's settings tree: guests only see Account settings; members see
 * the workspace feature pages, with admin-only configuration rendered read-only,
 * while Administration pages require the matching workspace permission.
 */
export type SettingsAccess = 'hidden' | 'read' | 'edit'

const PERSONAL_PAGES: ReadonlySet<SettingsPageId> = new Set([
  'preferences', 'shortcuts', 'profile', 'notifications', 'code-and-reviews', 'account-security', 'connections', 'agents',
])

/** Pages that are workspace-admin configuration but which members may view. */
const MEMBER_READ_ONLY_PAGES: ReadonlySet<SettingsPageId> = new Set([
  'sla', 'project-statuses', 'project-updates', 'initiatives', 'customer-requests',
])

/** Feature pages members may open; the page itself disables admin-only controls. */
const MEMBER_SELF_GATED_PAGES: ReadonlySet<SettingsPageId> = new Set([
  'ai', 'coding-sessions', 'coding-environments', 'loops', 'releases', 'pulse', 'asks', 'emojis', 'integrations',
])

export function isWorkspaceAdmin(data: Pick<BootstrapData, 'viewerRole'>): boolean {
  return data.viewerRole === 'admin' || data.viewerRole === 'owner'
}

export function isPersonalSettingsPage(page: SettingsPageId): boolean {
  return PERSONAL_PAGES.has(page)
}

/**
 * Workspace "who can" setting (Settings → Security), mirroring the API's
 * roleSatisfiesWorkspacePermission: members | owners_and_admins | admins |
 * owners | admins_only (plus legacy everyone/user). Unset values use `fallback`.
 */
export function roleAllowed(role: string | undefined, value: string | undefined, fallback = 'admins'): boolean {
  const normalized = (value || fallback).trim().toLowerCase()
  switch (normalized) {
    case 'members': case 'everyone': case 'user':
      return role === 'member' || role === 'admin' || role === 'owner'
    case 'owners':
      return role === 'owner'
    case 'admins_only':
      return role === 'admin'
    default:
      return role === 'admin' || role === 'owner'
  }
}

/** Whether a workspace member (non-admin) is granted the permission. */
export function membersAllowed(value: string | undefined, fallback = 'admins'): boolean {
  return roleAllowed('member', value, fallback)
}

export function workspaceSettingsAccess(data: Pick<BootstrapData, 'viewerRole' | 'workspaceSettings'>, page: SettingsPageId): SettingsAccess {
  if (PERSONAL_PAGES.has(page)) return 'edit'
  if (page === 'team') return 'edit'
  const role = data.viewerRole
  if (role !== 'member' && !isWorkspaceAdmin(data)) return 'hidden'
  const settings: Partial<WorkspaceSettings> = data.workspaceSettings ?? {}
  if (page === 'issue-labels' || page === 'project-labels' || page === 'initiative-labels')
    return roleAllowed(role, settings.labelPermission) ? 'edit' : 'read'
  if (page === 'issue-templates' || page === 'project-templates' || page === 'documents')
    return roleAllowed(role, settings.templatePermission) ? 'edit' : isWorkspaceAdmin(data) ? 'read' : 'hidden'
  if (page === 'import-export') return roleAllowed(role, settings.importPermission) || isWorkspaceAdmin(data) ? 'edit' : 'hidden'
  if (isWorkspaceAdmin(data)) return 'edit'
  if (MEMBER_READ_ONLY_PAGES.has(page)) return 'read'
  if (MEMBER_SELF_GATED_PAGES.has(page)) return 'edit'
  return 'hidden'
}

/** Linear's read-only notice for a workspace settings page a member can view. */
export function workspaceReadOnlyNotice(page: SettingsPageId): string {
  if (page === 'issue-labels' || page === 'project-labels' || page === 'initiative-labels') return 'Only admins can manage labels for this workspace'
  if (page === 'sla') return 'Only admins can edit SLAs'
  if (page === 'project-statuses') return 'Only admins can edit project statuses'
  if (page === 'project-updates' || page === 'integrations') return 'Only workspace admins can configure this'
  return 'Only workspace admins can modify this setting'
}

/**
 * Team settings: any member of the team (and workspace admins) can view every
 * section, like Linear's `team.canViewSettings`; editing follows the team's
 * permission for that section. Guests and non-members cannot open them.
 */
export function teamSettingsAccess(data: BootstrapData, teamId: string, section: TeamSettingsSection = 'overview'): SettingsAccess {
  if (isWorkspaceAdmin(data)) return 'edit'
  if (data.viewerRole !== 'member') return 'hidden'
  if (!viewerBelongsToTeam(data, teamId)) return 'hidden'
  return canManageTeamSettings(data, teamId, section) ? 'edit' : 'read'
}

function viewerBelongsToTeam(data: BootstrapData, teamId: string): boolean {
  const seen = new Set<string>()
  // Owners of a parent team administer its sub-teams, so they can view them too.
  for (let id = teamId, direct = true; id && !seen.has(id); id = data.teamSettings?.[id]?.parentTeamId ?? '', direct = false) {
    seen.add(id)
    const membership = (data.teamMembers ?? []).find(member => member.teamId === id && member.userId === data.viewer.id)
    if (membership && (direct || membership.role === 'owner')) return true
  }
  return false
}

/** Linear's read-only notice for a team settings section. */
export function teamReadOnlyNotice(section: TeamSettingsSection = 'overview'): string {
  switch (section) {
    case 'issue-labels':
    case 'project-labels':
      return 'Only admins and team owners can manage labels for this team'
    case 'templates':
    case 'recurring-issues':
      return 'Only admins and team owners can modify the team’s template settings'
    case 'cycles':
      return 'Only admins and team owners can modify the team’s cycle settings'
    case 'triage':
      return 'Only admins and team owners can modify triage settings'
    case 'agent-skills':
      return 'Only admins and team owners can create skills for this team'
    case 'agents':
      return 'Only admins and team owners can modify the team’s agent guidance prompts'
    case 'notifications':
      return 'Only admins and team owners can enable Slack notifications'
    case 'members':
      return 'Only admins and team owners can add or remove team members'
    default:
      return 'Only admins and team owners can modify this team’s settings'
  }
}

/** Release pipelines: any non-guest member can create one (Linear `canCreateReleasePipeline`). */
export function canCreateReleasePipeline(data: Pick<BootstrapData, 'viewerRole'>): boolean {
  return data.viewerRole === 'owner' || data.viewerRole === 'admin' || data.viewerRole === 'member'
}

/**
 * Linear `canAdministerPipeline`: workspace admins, or members who own every
 * team the pipeline belongs to. Pipelines without teams are admin-managed.
 */
export function canAdministerReleasePipeline(data: BootstrapData, pipeline: { teamIds: string[] }): boolean {
  if (isWorkspaceAdmin(data)) return true
  if (data.viewerRole !== 'member' || !pipeline.teamIds.length) return false
  return pipeline.teamIds.every(teamId => ownsTeam(data, teamId))
}

function ownsTeam(data: BootstrapData, teamId: string): boolean {
  const seen = new Set<string>()
  for (let id = teamId; id && !seen.has(id); id = data.teamSettings?.[id]?.parentTeamId ?? '') {
    seen.add(id)
    if ((data.teamMembers ?? []).some(member => member.teamId === id && member.userId === data.viewer.id && member.role === 'owner')) return true
  }
  return false
}
