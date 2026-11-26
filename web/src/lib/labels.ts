import type { IssueLabel, LabelGroup, LabelResourceType, Team } from '@/types/flow'
import type { TeamHierarchySettings } from './team-hierarchy'

export function labelTeamScopeIds(teamId: string, teams: Team[] = [], settings: TeamHierarchySettings = {}) {
  const result = [teamId]
  const seen = new Set([teamId])
  let parent = settings[teamId]?.parentTeamId
  while (parent && settings[parent] && !seen.has(parent)) {
    seen.add(parent)
    result.push(parent)
    parent = settings[parent]?.parentTeamId
  }
  return result
}

export function labelResourceType(label: IssueLabel): LabelResourceType {
	return label.resourceType === 'project' || label.resourceType === 'initiative' ? label.resourceType : 'issue'
}

export function labelsForResource(labels: IssueLabel[], resourceType: LabelResourceType, groups: LabelGroup[] = []) {
  const archivedGroups = new Set(groups.filter(group => group.archivedAt).map(group => group.id))
  return labels.filter(label => labelResourceType(label) === resourceType && !label.archivedAt && (!label.groupId || !archivedGroups.has(label.groupId)))
}

export function labelsForIssueTeam(labels: IssueLabel[], teamId?: string, groups: LabelGroup[] = [], ancestorTeamIds: string[] = []) {
  const scopes = new Set([teamId ?? '', ...ancestorTeamIds])
  return labelsForResource(labels, 'issue', groups).filter(label => isWorkspaceLabel(label) || scopes.has(label.scope ?? ''))
}

export function labelsForProject(labels: IssueLabel[], teamIds: string[], groups: LabelGroup[] = [], selectedIds: string[] = [], ancestorTeamIds: string[] = []) {
  const available = new Set(labelsForResource(labels, 'project', groups).map(label => label.id))
  const scopes = new Set([...teamIds, ...ancestorTeamIds])
  return labels.filter(label => labelResourceType(label) === 'project' && (available.has(label.id) || selectedIds.includes(label.id)) && (isWorkspaceLabel(label) || scopes.has(label.scope ?? '')))
}

let multiSelectGroupIds: ReadonlySet<string> = new Set()
let groupNames: ReadonlyMap<string, string> = new Map()

/**
 * Remembers which label groups are multi-select (their group type), so every label picker and bulk edit lets an item
 * carry several of those groups' labels, and the group names label chips sort and read by. The workspace data
 * registers its groups whenever they change.
 */
export function registerLabelGroups(groups: readonly Pick<LabelGroup, 'id' | 'name' | 'multiSelect'>[] = []) {
  multiSelectGroupIds = new Set(groups.filter(group => group.multiSelect).map(group => group.id))
  groupNames = new Map(groups.map(group => [group.id, group.name]))
}

export interface LabelChip<T> { key: string; name: string; colors: string[]; ariaLabel: string; labels: T[] }

/**
 * An item's label chips as the design reference shows them: labels of a group first (by group name, then label name),
 * then the others by name; the labels of one multi-select group share a chip ("Area: Web, API").
 */
export function labelChips<T extends { id: string; name: string; color: string; groupId?: string }>(labels: readonly T[]): LabelChip<T>[] {
  const byName = (left: string, right: string) => left.localeCompare(right, undefined, { sensitivity: 'base' })
  const sorted = [...labels].sort((left, right) => {
    const leftGroup = left.groupId ? groupNames.get(left.groupId) ?? '' : undefined, rightGroup = right.groupId ? groupNames.get(right.groupId) ?? '' : undefined
    if ((leftGroup === undefined) !== (rightGroup === undefined)) return leftGroup === undefined ? 1 : -1
    return byName(leftGroup ?? '', rightGroup ?? '') || byName(left.name, right.name)
  })
  const chips: LabelChip<T>[] = []
  for (const label of sorted) {
    const merged = label.groupId && isMultiSelectLabelGroup(label.groupId) ? chips.find(chip => chip.key === `group:${label.groupId}`) : undefined
    if (merged) { merged.labels.push(label); merged.colors.push(label.color); merged.name = merged.labels.map(item => item.name).join(', '); merged.ariaLabel = `${groupNames.get(label.groupId!) ?? ''}: ${merged.name}`; continue }
    const multi = label.groupId && isMultiSelectLabelGroup(label.groupId)
    chips.push({ key: multi ? `group:${label.groupId}` : label.id, name: label.name, colors: [label.color], ariaLabel: multi ? `${groupNames.get(label.groupId!) ?? ''}: ${label.name}` : label.name, labels: [label] })
  }
  return chips
}

/** A multi-select group: several of its labels may be applied together. Unknown groups are single-select. */
export function isMultiSelectLabelGroup(groupId?: string) {
  return Boolean(groupId && multiSelectGroupIds.has(groupId))
}

export function toggleGroupedLabelIds<T extends { id: string; groupId?: string }>(selectedIds: string[], labelId: string, labels: T[]) {
  if (selectedIds.includes(labelId)) return selectedIds.filter(id => id !== labelId)
  const target = labels.find(label => label.id === labelId)
  // A single-select group keeps one label: the chosen one replaces its peer.
  const withoutGroupPeer = target?.groupId && !isMultiSelectLabelGroup(target.groupId)
    ? selectedIds.filter(id => labels.find(label => label.id === id)?.groupId !== target.groupId)
    : selectedIds
  return [...withoutGroupPeer, labelId]
}

export function setGroupedLabelSelected<T extends { id: string; groupId?: string }>(selectedIds: string[], labelId: string, labels: T[], selected: boolean) {
  if (!selected) return selectedIds.filter(id => id !== labelId)
  if (selectedIds.includes(labelId)) return selectedIds
  return toggleGroupedLabelIds(selectedIds, labelId, labels)
}

export function isWorkspaceLabel(label: IssueLabel) {
  return !label.scope || label.scope.toLowerCase() === 'workspace'
}

export function labelScopeName(label: IssueLabel, teams: Team[] = []) {
  if (isWorkspaceLabel(label)) return 'Workspace'
  return teams.find(team => team.id === label.scope)?.name ?? 'Team'
}

export function groupsForResource(groups: LabelGroup[], resourceType: LabelResourceType) {
  return groups.filter(group => group.resourceType === resourceType && (!group.scope || group.scope.toLowerCase() === 'workspace'))
}

/** A bulk "Remove all labels" choice, passed where a label id is expected. */
export const REMOVE_ALL_LABELS = '__remove-all-labels__'
