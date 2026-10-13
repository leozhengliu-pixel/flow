import { describe, expect, it } from 'vitest'
import { makeBootstrap, makeIssue, project } from '@/test/fixtures'
import { issueBreadcrumbs, issueListContextPath, issueReturnPath } from './issue-navigation-context'
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
      { label: 'Test team', href: '/workspace/team/TST/overview', entity: true, team: expect.objectContaining({ color: expect.any(String) }) },
      { label: 'Issues', href: path },
    ])
  })

  it('shows "Projects › Project" for an issue opened from a project opened from a projects list, linking back to the tab it came from', () => {
    const linked = { ...issue, project }
    const tab = '/workspace/project/project-one/issues?projectMilestoneId=milestone-1'
    const trail = ['/workspace/projects/all', tab]
    expect(issueReturnPath({ navigationTrail: trail, returnTo: tab }, 'workspace', linked)).toBe(tab)
    expect(issueBreadcrumbs(data, linked, trail)).toEqual([
      { label: 'Projects', href: '/workspace/projects/all', tooltip: 'All projects' },
      { label: 'Project one', href: tab, entity: true, project },
    ])
  })

  it('shows only the project crumb when the project page was itself opened directly', () => {
    expect(issueBreadcrumbs(data, issue, '/workspace/project/project-one/overview')).toEqual([
      { label: 'Project one', href: '/workspace/project/project-one/overview', entity: true, project },
    ])
    expect(issueBreadcrumbs(data, issue, ['/workspace/my-issues/assigned', '/workspace/project/project-one/issues'])).toEqual([
      { label: 'Project one', href: '/workspace/project/project-one/issues', entity: true, project },
    ])
  })

  it('shows the issue a sub-issue was opened from after the root crumb, collapsing the middle like the reference', () => {
    const parent = makeIssue({ id: 'issue-2', identifier: 'TST-2', title: 'Parent issue' })
    const withParent = { ...data, issues: [...data.issues, parent] }
    const parentPath = '/workspace/issue/TST-2/parent-issue'
    const parentCrumb = { label: 'TST-2', href: parentPath, entity: true, tooltip: 'Parent issue', issue: parent }
    expect(issueBreadcrumbs(withParent, issue, ['/workspace/projects/all', '/workspace/project/project-one/issues', parentPath])).toEqual([
      { label: 'Projects', href: '/workspace/projects/all', tooltip: 'All projects' }, parentCrumb,
    ])
    expect(issueBreadcrumbs(withParent, issue, ['/workspace/project/project-one/issues', parentPath])).toEqual([
      { label: 'Project one', href: '/workspace/project/project-one/issues', entity: true, project }, parentCrumb,
    ])
    expect(issueBreadcrumbs(withParent, issue, ['/workspace/team/TST/all', parentPath])).toEqual([
      expect.objectContaining({ label: 'Test team', href: '/workspace/team/TST/overview' }), parentCrumb,
    ])
    expect(issueBreadcrumbs(withParent, issue, [parentPath])).toEqual([parentCrumb])
    // Esc and the last crumb go back to the parent.
    expect(issueReturnPath({ navigationTrail: ['/workspace/team/TST/all', parentPath] }, 'workspace', issue)).toBe(parentPath)
  })

  it('ignores trail entries for the issue itself (reloads, canonical slugs)', () => {
    expect(issueBreadcrumbs(data, issue, ['/workspace/team/TST/all', '/workspace/issue/TST-1/old-title'])).toEqual([
      expect.objectContaining({ label: 'Test team' }), { label: 'Issues', href: '/workspace/team/TST/all' },
    ])
    expect(issueReturnPath({ returnTo: '/workspace/issue/TST-1/old-title' }, 'workspace', issue)).toBe('/workspace/team/TST/all')
  })

  it('keeps personal navigation only for an actual personal list origin', () => {
    expect(issueBreadcrumbs(data, issue, '/workspace/my-issues/created')).toEqual([{ label: 'My issues', href: '/workspace/my-issues/created' }])
    expect(issueReturnPath(undefined, 'workspace', issue)).toBe('/workspace/team/TST/all')
    expect(issueBreadcrumbs(data, issue)).not.toContainEqual(expect.objectContaining({ label: 'My issues' }))
  })

  it('has no breadcrumb context when the issue was opened directly by URL, even when it belongs to a project', () => {
    expect(issueListContextPath(undefined, 'workspace')).toBeUndefined()
    expect(issueListContextPath({ returnTo: '/workspace/issue/TST-2/other' }, 'workspace')).toBeUndefined()
    expect(issueListContextPath({ returnTo: '/workspace/team/TST/all' }, 'workspace')).toBe('/workspace/team/TST/all')
    expect(issueBreadcrumbs(data, issue)).toEqual([])
    expect(issueBreadcrumbs(data, { ...issue, project }, undefined)).toEqual([])
  })

  it('rejects external, other-workspace and unknown return targets', () => {
    for (const returnTo of ['https://example.com', '//example.com', '/other/team/TST/all', '/workspace/../other/team/TST/all', '/workspace/missing']) {
      expect(issueReturnPath({ returnTo }, 'workspace', issue)).toBe('/workspace/team/TST/all')
    }
  })

  it('retains saved view names and return paths', () => {
    const saved = { id: 'view-1', slugId: 'planned-work', name: 'Planned work' } as BootstrapDataView
    expect(issueBreadcrumbs({ ...data, savedViews: [saved] }, issue, '/workspace/view/planned-work')).toEqual([{ label: 'Planned work', href: '/workspace/view/planned-work', entity: true }])
  })
})

type BootstrapDataView = ReturnType<typeof makeBootstrap>['savedViews'][number]
