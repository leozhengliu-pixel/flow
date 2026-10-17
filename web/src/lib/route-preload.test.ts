import { describe, expect, it, vi } from 'vitest'
import { parseAppRoute } from './app-routes'
import { clientNavigationTarget, pagesForRoute, preloadInitialRoute } from './route-preload'
import * as pages from './route-pages'

describe('route preloading', () => {
  it.each([
    ['/acme/my-issues/assigned', 'MyIssuesPage'], ['/acme/issues/all', 'IssueExplorerPage'],
    ['/acme/views/issues', 'ViewsPage'], ['/acme/views/projects/new', 'ProjectsPage'],
    ['/acme/team/ENG/views/issues/new', 'IssueExplorerPage'], ['/acme/projects/all', 'ProjectsPage'],
    ['/acme/settings/account/security', 'SettingsPage'], ['/acme/inbox', 'InboxAppPage'],
    ['/acme/documents', 'TeamOverviewPage'], ['/acme/teams', 'WorkspaceDirectoryPage'],
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
  it('starts the current URL\'s page chunks at startup without waiting for data', () => {
    const preload = vi.spyOn(pages.IssueExplorerPage, 'preload').mockResolvedValue()
    const other = vi.spyOn(pages.ProjectsPage, 'preload').mockResolvedValue()
    preloadInitialRoute({ pathname: '/acme/team/ENG/all', search: '' })
    expect(preload).toHaveBeenCalledTimes(1)
    expect(other).not.toHaveBeenCalled()
    preload.mockRestore(); other.mockRestore()
  })
  it('swallows a failed startup preload', async () => {
    const preload = vi.spyOn(pages.IssueExplorerPage, 'preload').mockRejectedValue(new Error('offline'))
    const unhandled = vi.fn()
    process.on('unhandledRejection', unhandled)
    expect(() => preloadInitialRoute({ pathname: '/acme/issues/all', search: '' })).not.toThrow()
    await new Promise(resolve => setTimeout(resolve, 10))
    process.off('unhandledRejection', unhandled)
    expect(preload).toHaveBeenCalledTimes(1)
    expect(unhandled).not.toHaveBeenCalled()
    preload.mockRestore()
  })
})
