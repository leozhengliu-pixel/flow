import { describe, expect, it } from 'vitest'
import { parseAppRoute } from './app-routes'
import { clientNavigationTarget, pagesForRoute } from './route-preload'

describe('route preloading', () => {
  it.each([
    ['/acme/my-issues/assigned', 'MyIssuesPage'], ['/acme/issues/all', 'IssueExplorerPage'],
    ['/acme/views/issues', 'ViewsPage'], ['/acme/views/projects/new', 'ProjectsPage'],
    ['/acme/team/ENG/views/issues/new', 'IssueExplorerPage'], ['/acme/projects/all', 'ProjectsPage'],
    ['/acme/settings/account/security', 'SettingsPage'], ['/acme/inbox', 'InboxAppPage'],
    ['/acme/documents', 'DocumentsIndexPage'], ['/acme/teams', 'WorkspaceDirectoryPage'],
    ['/acme/members', 'WorkspaceDirectoryPage'], ['/acme/reviews', 'ReviewsPage'],
    ['/acme/initiatives', 'InitiativesPage'], ['/acme/agent', 'AgentPage'],
  ])('maps %s to the module rendered by the route', (path, module) => {
    expect(pagesForRoute(parseAppRoute(path, ''))).toContain(module)
  })
  it('keeps query parameters and cross-workspace routes', () => {
    const a = document.createElement('a'); a.href = '/other/issues/all?status=active'
    expect(clientNavigationTarget(a, new URL('http://localhost/acme/projects/all'))).toBe('/other/issues/all?status=active')
  })
  it.each(['https://example.com/acme/issues/all', 'mailto:hello@example.com', '/api/export', '/login', '#section', 'http://[invalid'])('leaves native destination %s alone', href => {
    const a = document.createElement('a'); a.setAttribute('href', href)
    expect(clientNavigationTarget(a, new URL('http://localhost/acme/projects/all'))).toBeUndefined()
  })
  it.each(['download', 'data-native-navigation'])('respects %s', attribute => {
    const a = document.createElement('a'); a.href = '/acme/issues/all'; a.setAttribute(attribute, '')
    expect(clientNavigationTarget(a, new URL('http://localhost/acme/projects/all'))).toBeUndefined()
  })
  it('respects new-tab targets', () => {
    const a = document.createElement('a'); a.href = '/acme/issues/all'; a.target = '_blank'
    expect(clientNavigationTarget(a, new URL('http://localhost/acme/projects/all'))).toBeUndefined()
  })
})
