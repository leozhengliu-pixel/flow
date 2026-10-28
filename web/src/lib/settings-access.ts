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
 * Mirrors the reference app's settings tree: guests only see Account settings; members see
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
  'ai', 'ai-agent', 'code-intelligence', 'triage-intelligence', 'ai-summaries', 'ai-updates', 'installed-agents', 'coding-sessions', 'coding-environments', 'loops', 'releases', 'pulse', 'asks', 'emojis', 'integrations',
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

/**
 * The reference app answers a workspace settings URL the viewer may not open with its
 * "Not found" page; Settings › API names the reason.
 */
export function hiddenSettingsMessage(page: SettingsPageId): string {
  return page === 'api' ? 'Only workspace administrators can manage API settings' : 'We could not find the page you were looking for'
}

export type AdminHintControl = { label: string; kind: 'toggle' | 'select' | 'button' | 'field' }

/**
 * Whether disabled controls on a workspace settings page explain themselves
 * with the reference app's admin-only hover hint: read-only pages and the feature pages
 * that disable their admin-only controls for members.
 */
export function adminHintsApply(data: Pick<BootstrapData, 'viewerRole' | 'workspaceSettings'>, page: SettingsPageId): boolean {
  if (data.viewerRole !== 'member' || PERSONAL_PAGES.has(page) || page === 'team') return false
  const access = workspaceSettingsAccess(data, page)
  // Releases and emojis are member features; pipelines explain their own owner rules.
  return access === 'read' || (access === 'edit' && MEMBER_SELF_GATED_PAGES.has(page) && page !== 'releases' && page !== 'emojis')
}

/**
 * The hover hint on a disabled control a member cannot use (the reference app shows it
 * with a "View admins →" link instead of a page banner). Read-only pages
 * explain every control; feature pages only their admin-only toggles and
 * pickers plus the named actions. Null when the control is disabled for
 * another reason.
 */
export function adminOnlyHint(page: SettingsPageId, control: AdminHintControl, readOnlyPage: boolean): string | null {
  const label = control.label.trim()
  switch (page) {
    case 'sla':
      if (label === 'Add rule') return 'Only admins can add a rule'
      if (label === 'Edit configuration') return 'Only admins can edit SLAs'
      if (label === 'Delete configuration') return 'Only admins can delete SLAs'
      break
    case 'project-statuses':
      if (control.kind === 'button') return 'Only workspace admins can edit project statuses'
      break
    case 'customer-requests':
      if (label === 'Create new customer status') return 'Only workspace admins can edit customer statuses'
      break
    case 'project-updates':
    case 'initiatives':
      if (label === 'Connect') return 'Only workspace admins can configure this'
      break
    case 'asks':
      if (label === 'Connect workspace') return 'Only admins can connect a workspace'
      if (label === 'Add email') return 'Only admins can add an email address'
      break
    case 'loops':
      if (/allowlist|trusted source|allowed source/i.test(label)) return 'Only workspace security admins can change sources'
      break
    case 'integrations':
      if (label === 'Disable') return 'Only admins can disable this integration'
      if (label === 'Enable') return 'Only admins can enable this integration'
      break
    case 'pulse':
      // Members choose their own summary schedule; it is only off while Pulse is.
      if (label === 'Your personal schedule') return null
      break
    case 'issue-labels':
    case 'project-labels':
    case 'initiative-labels':
      if (readOnlyPage) return 'Only admins can manage labels for this workspace'
      break
  }
  if (readOnlyPage || control.kind === 'toggle' || control.kind === 'select') return 'Only admins can modify'
  return null
}

/** The reference app's read-only notice for a workspace settings page a member can view. */
export function workspaceReadOnlyNotice(page: SettingsPageId): string {
  if (page === 'issue-labels' || page === 'project-labels' || page === 'initiative-labels') return 'Only admins can manage labels for this workspace'
  if (page === 'sla') return 'Only admins can edit SLAs'
  if (page === 'project-statuses') return 'Only admins can edit project statuses'
  if (page === 'project-updates' || page === 'integrations') return 'Only workspace admins can configure this'
  return 'Only workspace admins can modify this setting'
}

/**
 * Team settings: any member of the team (and workspace admins) can view every
 * section `team.canViewSettings`; editing follows the team's
 * permission for that section. Guests and non-members cannot open them.
 */
export function teamSettingsAccess(data: BootstrapData, teamId: string, section: TeamSettingsSection = 'overview'): SettingsAccess {
  if (isWorkspaceAdmin(data)) return 'edit'
  if (data.viewerRole !== 'member') return 'hidden'
  if (!viewerBelongsToTeam(data, teamId)) return 'hidden'
  const editable = canManageTeamSettings(data, teamId, section)
  // The reference app sends members who do not own the team from "Access and
  // permissions" back to the team overview.
  if (section === 'security' && !editable) return 'hidden'
  return editable ? 'edit' : 'read'
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

/** The reference app's read-only notice for a team settings section. */
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
      return 'Only admins and team owners can enable {name} notifications'
    case 'members':
      return 'Only admins and team owners can add or remove team members'
    default:
      return 'Only admins and team owners can modify this team’s settings'
  }
}

/** Release pipelines: any non-guest member can create one (the reference app `canCreateReleasePipeline`). */
export function canCreateReleasePipeline(data: Pick<BootstrapData, 'viewerRole'>): boolean {
  return data.viewerRole === 'owner' || data.viewerRole === 'admin' || data.viewerRole === 'member'
}

/**
 * The reference app `canAdministerPipeline`: workspace admins, or members who own every
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
