import { describe, expect, it } from 'vitest'
import { boundedIssueSequence, issueSequenceIDs, navigationReturnPath, navigationTrail, nextNavigationState, projectsListOriginPath, reviewsOriginView, sidebarOriginPath, workspacePath } from './navigation-context'
import { parseAppRoute } from './app-routes'
import { sidebarRoutePath } from './sidebar-route'

const location = (path: string, state?: unknown) => { const url = new URL(path, 'https://flow.invalid'); return { pathname: url.pathname, search: url.search, hash: url.hash, state } }

describe('navigation contexts across modules', () => {
  it.each([
    ['/acme/team/ENG/projects/all?status=planned', '/acme/project/example/overview', '/acme/project/example/issues'],
    ['/acme/team/ENG/initiatives/planned', '/acme/initiative/example/overview', '/acme/initiative/example/projects'],
    ['/acme/reviews/created?repository=one', '/acme/review/example', '/acme/review/example/changes'],
    ['/acme/documents?query=notes', '/acme/document/notes', '/acme/document/notes#section'],
    ['/acme/team/ENG/all?status=todo', '/acme/settings/account/preferences', '/acme/settings/account/security'],
    ['/acme/customers?query=one', '/acme/customer/one-123456789012', '/acme/customer/one-123456789012#requests'],
    ['/acme/release-pipelines', '/acme/pipeline/mobile/release/v1/issues', '/acme/pipeline/mobile/release/v1/release-notes'],
    ['/acme/loops', '/acme/loops/example', '/acme/loops/example#settings'],
    ['/acme/team/ENG/cycles', '/acme/team/ENG/cycle/active', '/acme/team/ENG/cycle/active#progress'],
  ])('preserves %s through detail tab changes and returns without a loop', (source, detail, tab) => {
    const opened = nextNavigationState(location(source), detail)
    const changed = nextNavigationState(location(detail, opened), tab)
    expect(navigationReturnPath(changed, 'acme', '')).toBe(source)
    const returned = nextNavigationState(location(tab, changed), source)
    expect(navigationTrail(returned, 'acme')).toEqual([])
  })

  it('reports a projects-list origin only when the project was opened from a projects list', () => {
    const list = '/acme/projects/all'
    const opened = nextNavigationState(location(list), '/acme/project/p/overview')
    expect(projectsListOriginPath(opened, 'acme')).toBe(list)
    const tabbed = nextNavigationState(location('/acme/project/p/overview', opened), '/acme/project/p/issues')
    expect(projectsListOriginPath(tabbed, 'acme')).toBe(list)
    expect(projectsListOriginPath(nextNavigationState(location('/acme/team/ENG/projects/all'), '/acme/project/p/overview'), 'acme')).toBe('/acme/team/ENG/projects/all')
    expect(projectsListOriginPath(null, 'acme')).toBeUndefined()
    expect(projectsListOriginPath(nextNavigationState(location('/acme/my-issues/assigned'), '/acme/project/p/overview'), 'acme')).toBeUndefined()
    expect(projectsListOriginPath(nextNavigationState(location('/acme/issue/ENG-1/test'), '/acme/project/p/overview'), 'acme')).toBeUndefined()
  })

  it('pops nested project, issue and document contexts one level at a time', () => {
    const list = '/acme/team/ENG/projects/all?filter=active'
    const project = '/acme/project/p/issues'
    const issue = '/acme/issue/ENG-1/test'
    const document = '/acme/document/spec'
    const projectState = nextNavigationState(location(list), project)
    const issueState = nextNavigationState(location(project, projectState), issue)
    const docState = nextNavigationState(location(issue, issueState), document)
    expect(navigationTrail(docState, 'acme')).toEqual([list, project, issue])
    const returned = nextNavigationState(location(document, docState), issue)
    expect(navigationReturnPath(returned, 'acme', '')).toBe(project)
    expect(sidebarOriginPath(returned, issue, 'acme')).toBe(list)
  })

  it('keeps the issue list and sequence through sibling navigation and slug canonicalization', () => {
    const state = nextNavigationState(location('/acme/team/ENG/all?status=todo'), '/acme/issue/ENG-1/old', { issueSequence: ['i1','i2'] })
    const next = nextNavigationState(location('/acme/issue/ENG-1/old', state), '/acme/issue/ENG-2/title', { issueSibling: true })
    expect(next).not.toHaveProperty('issueSibling', true)
    const canonical = nextNavigationState(location('/acme/issue/ENG-2/title', next), '/acme/issue/ENG-2/new-title', undefined, true)
    expect(canonical).toMatchObject({ returnTo: '/acme/team/ENG/all?status=todo', issueSequence: ['i1','i2'] })
    const settings = nextNavigationState(location('/acme/issue/ENG-2/new-title', canonical), '/acme/settings/account/preferences')
    const returned = nextNavigationState(location('/acme/settings/account/preferences', settings), '/acme/issue/ENG-2/new-title')
    expect(returned).toMatchObject({ issueSequence: ['i1','i2'], returnTo: '/acme/team/ENG/all?status=todo' })
  })

  it('records the issue a sub-issue (or any linked issue) was opened from, dropping the list sequence', () => {
    const list = '/acme/project/p/issues'
    const parent = '/acme/issue/ENG-1/parent'
    const opened = nextNavigationState(location(list, { navigationTrail: ['/acme/projects/all'] }), parent, { issueSequence: ['i1', 'i2'] })
    expect(navigationTrail(opened, 'acme')).toEqual(['/acme/projects/all', list])
    const child = nextNavigationState(location(parent, opened), '/acme/issue/ENG-3/child')
    expect(navigationTrail(child, 'acme')).toEqual(['/acme/projects/all', list, parent])
    expect(issueSequenceIDs(child)).toEqual([])
    // The parent crumb is an ancestor: going back to it restores the parent's own trail.
    const back = nextNavigationState(location('/acme/issue/ENG-3/child', child), parent)
    expect(navigationTrail(back, 'acme')).toEqual(['/acme/projects/all', list])
    // Triage walks its list with an explicit return target: still a sibling step.
    const triage = nextNavigationState(location(parent, opened), '/acme/issue/ENG-2/next', { returnTo: list, issueSequence: ['i1', 'i2'] })
    expect(navigationTrail(triage, 'acme')).toEqual(['/acme/projects/all', list])
  })

  it('clears context when changing workspaces or explicitly resetting navigation', () => {
    const current = location('/acme/issue/ENG-1/title', { returnTo: '/acme/team/ENG/all' })
    expect(nextNavigationState(current, '/other/projects/all')).toBeUndefined()
    expect(nextNavigationState(current, '/acme/projects/all', null)).toBeNull()
    for (const path of ['//evil.test', '/acme/../other/projects/all', '/acme/unknown', '/acme\\../other/projects/all']) expect(workspacePath(path, 'acme')).toBeUndefined()
  })

  it('bounds retained history and navigation sequences without excluding the clicked issue', () => {
    const ids = Array.from({ length: 5000 }, (_, i) => `issue-${i}`)
    const sequence = boundedIssueSequence(ids, 'issue-4000')
    expect(sequence).toHaveLength(1000)
    expect(sequence).toContain('issue-4000')
    expect(navigationTrail({ navigationTrail: Array.from({ length: 50 }, (_, i) => `/acme/document/doc-${i}`) }, 'acme')).toHaveLength(12)
  })

  it('restores the created review list with query parameters', () => {
    expect(reviewsOriginView({ returnTo: '/acme/reviews/created?filter=mine' }, 'acme')).toBe('created')
  })

  it.each([
    ['/acme/team/ENG/active', '/acme/team/ENG/all'],
    ['/acme/team/QA/cycle/active', '/acme/team/QA/cycles'],
    ['/acme/team/ENG/projects/all', '/acme/team/ENG/projects/all'],
    ['/acme/team/QA/initiatives/planned', '/acme/team/QA/initiatives/active'],
    ['/acme/my-issues/created', '/acme/my-issues/assigned'],
    ['/acme/reviews/created', '/acme/reviews'],
  ])('selects only the matching sidebar entry for %s', (path, selected) => {
    expect(sidebarRoutePath(parseAppRoute(path))).toBe(selected)
  })
})
