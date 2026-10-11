import { describe, expect, it } from 'vitest'
import type { BootstrapData, IssueTemplate } from '@/types/flow'
import { isWorkspaceIssueTemplate, issueTemplateOwnerTeamId, issueTemplatesForTeam, teamOwnedIssueTemplates, workspaceIssueTemplates } from './issue-templates'

const template = (id: string, over: Partial<IssueTemplate>) => ({ id, name: id, priority: 0, labelIds: [], ...over }) as IssueTemplate
const workspace = template('workspace', { scope: 'workspace', teamId: 'team-a' })
const narrowed = template('narrowed', { scope: 'team', visibilityTeamId: 'team-b' })
const parentTeam = template('parent', { teamId: 'team-parent' })
const own = template('own', { teamId: 'team-a' })
const other = template('other', { teamId: 'team-c' })
const all = [workspace, narrowed, parentTeam, own, other]

describe('issue template ownership', () => {
  it('treats workspace templates as workspace-wide even when they set a default team', () => {
    expect(isWorkspaceIssueTemplate(workspace)).toBe(true)
    expect(issueTemplateOwnerTeamId(workspace)).toBeUndefined()
    expect(workspaceIssueTemplates(all)).toEqual([workspace])
  })

  it('assigns team templates and visibility-narrowed workspace templates to their team', () => {
    expect(issueTemplateOwnerTeamId(own)).toBe('team-a')
    expect(issueTemplateOwnerTeamId(narrowed)).toBe('team-b')
    expect(teamOwnedIssueTemplates(all, ['team-a'])).toEqual([own])
  })

  it('offers a team its own, its parents\' and the workspace templates', () => {
    const data = { issueTemplates: all, teams: [], teamSettings: { 'team-a': { parentTeamId: 'team-parent' }, 'team-parent': {} } } as unknown as BootstrapData
    expect(issueTemplatesForTeam(data, 'team-a').map(item => item.id)).toEqual(['parent', 'own', 'workspace'])
  })
})
