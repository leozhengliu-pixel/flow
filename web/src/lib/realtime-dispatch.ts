import { requiresIssueVisibilityCheck } from '@/lib/issue-detail-cache'
import { metadataOnlyRealtimeEvent } from '@/lib/workspace-metadata-refresh'
import { canApplyRealtimePatch, isLabelStateRenameEvent } from '@/store/apply-realtime-patch'
import type { RealtimeEvent } from '@/types/flow'

/**
 * What the client does with a realtime event it cannot answer from a more
 * specific handler. `resync` is the only action that downloads the whole
 * bootstrap: overflow, an expired cursor, or an event this client does not know.
 */
export type RealtimeAction =
  | { kind: 'patch' }
  | { kind: 'refetch'; issueIds: string[]; removedIssueIds: string[] }
  | { kind: 'metadata'; visibility: boolean }
  | { kind: 'resync' }

// Derived-field and directory events of a paged workspace: they change the
// small metadata projection (states, labels, teams, members, users, cycles, ...)
// and never the issue rows themselves, so the slim paged bootstrap is enough.
const PAGED_METADATA_EVENT = new RegExp('^(?:' + [
  'user\\.[a-z_]+', 'workspace\\.(?!resync_required$)[a-z_]+', 'workspace_member\\.[a-z_]+', 'membership\\.[a-z_]+',
  'team\\.[a-z_]+', 'team_member\\.[a-z_]+', 'workflow_state\\.[a-z_]+', 'label\\.[a-z_]+', 'label_group\\.[a-z_]+', 'issue_label\\.[a-z_]+',
  'cycle\\.(?:started|completed)', 'sla_[a-z_]+\\.[a-z_]+', 'triage_[a-z_]+\\.[a-z_]+', 'custom_emoji\\.[a-z_]+',
  'issue_permission\\.[a-z_]+',
].join('|') + ')$')

const COSMETIC_TEAM_FIELDS = ['name', 'icon', 'color', 'description', 'timezone', 'showInitiatives']
const ACCESS_TEAM_FIELDS = ['access', 'private', 'retired', 'parentTeamId', 'membershipRestriction']

/** A team rename/recolor cannot change which issues the viewer may see. */
export function cosmeticTeamChange(event: RealtimeEvent): boolean {
  if (event.type !== 'team.updated' && event.type !== 'team.settings_updated') return false
  const fields = event.payload
  return Boolean(fields) && COSMETIC_TEAM_FIELDS.some(key => Object.hasOwn(fields!, key)) && !ACCESS_TEAM_FIELDS.some(key => Object.hasOwn(fields!, key))
}

function ids(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((id): id is string => typeof id === 'string') : []
}

export function classifyRealtimeEvent(event: RealtimeEvent, paged: boolean): RealtimeAction {
  if (event.type === 'resync' || event.type === 'workspace.resync_required') return { kind: 'resync' }
  if (canApplyRealtimePatch(event)) return { kind: 'patch' }
  const removedIssueIds = ids(event.payload?.removedIssueIds)
  const issueIds = ids(event.payload?.issueIds)
  if (issueIds.length && /^(issue\.|attachment\.)/.test(event.type)) return { kind: 'refetch', issueIds, removedIssueIds }
  // Renames only change the label/state copies on held issues: never a full bootstrap, paged or not.
  if (isLabelStateRenameEvent(event)) return { kind: 'metadata', visibility: false }
  if (metadataOnlyRealtimeEvent(event)) return { kind: 'metadata', visibility: false }
  if (paged && PAGED_METADATA_EVENT.test(event.type)) return { kind: 'metadata', visibility: requiresIssueVisibilityCheck(event.type) && !cosmeticTeamChange(event) }
  return { kind: 'resync' }
}
