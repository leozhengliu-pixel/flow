import type { IssueLabel, LabelGroup, LabelResourceType, Team } from '@/types/flow'

export function labelResourceType(label: IssueLabel): LabelResourceType {
	return label.resourceType === 'project' || label.resourceType === 'initiative' ? label.resourceType : 'issue'
}

export function labelsForResource(labels: IssueLabel[], resourceType: LabelResourceType) {
  return labels.filter(label => labelResourceType(label) === resourceType && !label.archivedAt)
}

export function labelsForIssueTeam(labels: IssueLabel[], teamId?: string) {
  return labelsForResource(labels, 'issue').filter(label => isWorkspaceLabel(label) || label.scope === teamId)
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
