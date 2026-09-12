import { describe, expect, it } from 'vitest'
import { makeBootstrap, makeIssue, project } from '@/test/fixtures'
import { issueBreadcrumbs, issueReturnPath } from './issue-navigation-context'
import { parseAppRoute, teamIssuesPath } from './app-routes'

describe('issue navigation context', () => {
  const issue = makeIssue()
  const data = makeBootstrap({ savedViews: [] })

  it('treats the reported team URL as a team route, not a personal route', () => {
    expect(parseAppRoute('/haier/team/HAI/all')).toEqual({ kind: 'team-issues', workspaceSlug: 'haier', teamKey: 'HAI', view: 'all' })
    expect(teamIssuesPath('haier', 'HAI')).toBe('/haier/team/HAI/all')
  })

  it('retains team context and filters even when the issue belongs to a project', () => {
    const path = '/workspace/team/TST/backlog?status=state-backlog'
    const linked = { ...issue, project }
    expect(issueReturnPath({ returnTo: path }, 'workspace', linked)).toBe(path)
    expect(issueBreadcrumbs(data, linked, path)).toEqual([
      { label: 'Test team', href: '/workspace/team/TST/overview', entity: true },
      { label: 'Issues', href: path },
    ])
  })

  it('links the project breadcrumb to the project overview instead of the issues tab used to open the issue', () => {
    const linked = { ...issue, project }
    const path = '/workspace/project/project-one/issues?projectMilestoneId=milestone-1'
    expect(issueReturnPath({ returnTo: path }, 'workspace', linked)).toBe(path)
    expect(issueBreadcrumbs(data, linked, path)).toEqual([
      { label: 'Project one', href: '/workspace/project/project-one/overview', entity: true },
    ])
  })

  it('keeps personal navigation only for an actual personal list origin', () => {
    expect(issueBreadcrumbs(data, issue, '/workspace/my-issues/created')).toEqual([{ label: 'My issues', href: '/workspace/my-issues/created' }])
    expect(issueReturnPath(undefined, 'workspace', issue)).toBe('/workspace/team/TST/all')
    expect(issueBreadcrumbs(data, issue)).not.toContainEqual(expect.objectContaining({ label: 'My issues' }))
  })

  it('rejects external, other-workspace and issue-detail return targets', () => {
    for (const returnTo of ['https://example.com', '//example.com', '/other/team/TST/all', '/workspace/../other/team/TST/all', '/workspace/issue/TST-2/another', '/workspace/missing']) {
      expect(issueReturnPath({ returnTo }, 'workspace', issue)).toBe('/workspace/team/TST/all')
    }
  })

  it('retains saved view names and return paths', () => {
    const saved = { id: 'view-1', slugId: 'planned-work', name: 'Planned work' } as BootstrapDataView
    expect(issueBreadcrumbs({ ...data, savedViews: [saved] }, issue, '/workspace/view/planned-work')).toEqual([{ label: 'Planned work', href: '/workspace/view/planned-work', entity: true }])
  })
})

type BootstrapDataView = ReturnType<typeof makeBootstrap>['savedViews'][number]
