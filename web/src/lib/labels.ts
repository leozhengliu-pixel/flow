import type { IssueLabel, LabelGroup, LabelResourceType, Team } from '@/types/flow'

export function labelResourceType(label: IssueLabel): LabelResourceType {
	return label.resourceType === 'project' || label.resourceType === 'initiative' ? label.resourceType : 'issue'
}

export function labelsForResource(labels: IssueLabel[], resourceType: LabelResourceType, groups: LabelGroup[] = []) {
  const archivedGroups = new Set(groups.filter(group => group.archivedAt).map(group => group.id))
  return labels.filter(label => labelResourceType(label) === resourceType && !label.archivedAt && (!label.groupId || !archivedGroups.has(label.groupId)))
}

export function labelsForIssueTeam(labels: IssueLabel[], teamId?: string, groups: LabelGroup[] = []) {
  return labelsForResource(labels, 'issue', groups).filter(label => isWorkspaceLabel(label) || label.scope === teamId)
}

export function toggleGroupedLabelIds<T extends { id: string; groupId?: string }>(selectedIds: string[], labelId: string, labels: T[]) {
  if (selectedIds.includes(labelId)) return selectedIds.filter(id => id !== labelId)
  const target = labels.find(label => label.id === labelId)
  const withoutGroupPeer = target?.groupId
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

export function groupsForResource(groups: LabelGroup[], resourceType: 'issue' | 'project') {
  return groups.filter(group => group.resourceType === resourceType && (!group.scope || group.scope.toLowerCase() === 'workspace'))
}
