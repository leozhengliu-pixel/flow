import { describe, expect, it } from 'vitest'
import type { IssueLabel, LabelGroup, Team } from '@/types/flow'
import { groupsForResource, isWorkspaceLabel, labelResourceType, labelScopeName, labelsForIssueTeam, labelsForProject, labelsForResource, setGroupedLabelSelected, toggleGroupedLabelIds } from './labels'

const labels = [
  { id: 'issue-a', name: 'A', scope: 'Workspace', resourceType: 'issue', groupId: 'issue-group' },
  { id: 'issue-b', name: 'B', scope: 'team-a', resourceType: 'issue', groupId: 'issue-group' },
  { id: 'project-a', name: 'Project', scope: 'Workspace', resourceType: 'project' },
  { id: 'archived', name: 'Archived', scope: 'Workspace', resourceType: 'issue', archivedAt: '2026-01-01' },
] as IssueLabel[]

const groups = [
  { id: 'issue-group', name: 'Issue group', resourceType: 'issue', scope: 'Workspace' },
  { id: 'archived-group', name: 'Archived group', resourceType: 'issue', scope: 'Workspace', archivedAt: '2026-01-01' },
  { id: 'project-group', name: 'Project group', resourceType: 'project', scope: 'Workspace' },
] as LabelGroup[]

describe('label helpers', () => {
  it('only offers project labels from the workspace and the project teams', () => {
    const options = [...labels, { id: 'project-team-a', name: 'Team A', resourceType: 'project', scope: 'team-a' }, { id: 'project-team-b', name: 'Team B', resourceType: 'project', scope: 'team-b' }] as IssueLabel[]
    expect(labelsForProject(options, ['team-a'], groups).map(label => label.id)).toEqual(['project-a', 'project-team-a'])
  })
  it('retains applied archived project labels so other edits do not silently remove them', () => {
    const archived = { id: 'old', name: 'Old', resourceType: 'project', archivedAt: '2026-01-01' } as IssueLabel
    expect(labelsForProject([archived], [], [], []).length).toBe(0)
    expect(labelsForProject([archived], [], [], ['old'])).toEqual([archived])
  })
  it('normalizes resource types and excludes archived records', () => {
    expect(labelResourceType({ resourceType: 'initiative' } as IssueLabel)).toBe('initiative')
    expect(labelResourceType({ resourceType: 'unknown' } as unknown as IssueLabel)).toBe('issue')
    expect(labelsForResource(labels, 'issue', groups).map(label => label.id)).toEqual(['issue-a', 'issue-b'])
    expect(labelsForResource(labels, 'project', groups).map(label => label.id)).toEqual(['project-a'])
  })

  it('respects workspace and team scopes', () => {
    expect(labelsForIssueTeam(labels, 'team-a', groups).map(label => label.id)).toEqual(['issue-a', 'issue-b'])
    expect(labelsForIssueTeam(labels, 'team-b', groups).map(label => label.id)).toEqual(['issue-a'])
    expect(isWorkspaceLabel({ scope: '' } as IssueLabel)).toBe(true)
    expect(labelScopeName(labels[1], [{ id: 'team-a', name: 'Platform' } as Team])).toBe('Platform')
    expect(labelScopeName({ scope: 'missing' } as IssueLabel)).toBe('Team')
  })

  it('enforces one selected label per group', () => {
    expect(toggleGroupedLabelIds(['issue-a'], 'issue-b', labels)).toEqual(['issue-b'])
    expect(toggleGroupedLabelIds(['issue-a'], 'issue-a', labels)).toEqual([])
    expect(setGroupedLabelSelected(['issue-a'], 'issue-b', labels, true)).toEqual(['issue-b'])
    expect(setGroupedLabelSelected(['issue-a'], 'issue-a', labels, true)).toEqual(['issue-a'])
    expect(setGroupedLabelSelected(['issue-a'], 'issue-a', labels, false)).toEqual([])
  })

  it('returns only workspace groups for the requested resource', () => {
    expect(groupsForResource(groups, 'issue').map(group => group.id)).toEqual(['issue-group', 'archived-group'])
    expect(groupsForResource(groups, 'project').map(group => group.id)).toEqual(['project-group'])
  })
})
