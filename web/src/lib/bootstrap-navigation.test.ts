import { describe, expect, it } from 'vitest'
import type { BootstrapProjection } from './api'
import { bootstrapNavigationDecision, bootstrapRefreshProjection, bootstrapRequestKey, canReuseWorkspaceForIssueDetail } from './bootstrap-navigation'

const current = {
  workspace: { urlKey: 'acme' },
  viewer: { id: 'viewer-1' },
  resourceDetailsOmitted: false,
}
const fullKey = bootstrapRequestKey('viewer-1', 'acme')

describe('canReuseWorkspaceForIssueDetail', () => {
  it('reuses a complete workspace snapshot for an issue opened in place', () => {
    expect(canReuseWorkspaceForIssueDetail({ current, requestedWorkspaceKey: 'acme', viewerId: 'viewer-1', projection: 'issue-detail', loadedRequestKey: fullKey })).toBe(true)
  })

  it('requires the same viewer and a complete snapshot', () => {
    expect(canReuseWorkspaceForIssueDetail({ current, requestedWorkspaceKey: 'other', viewerId: 'viewer-1', projection: 'issue-detail', loadedRequestKey: fullKey })).toBe(false)
    expect(canReuseWorkspaceForIssueDetail({ current, requestedWorkspaceKey: 'acme', viewerId: 'viewer-2', projection: 'issue-detail', loadedRequestKey: fullKey })).toBe(false)
    expect(canReuseWorkspaceForIssueDetail({ current: { ...current, resourceDetailsOmitted: true }, requestedWorkspaceKey: 'acme', viewerId: 'viewer-1', projection: 'issue-detail', loadedRequestKey: fullKey })).toBe(false)
    expect(canReuseWorkspaceForIssueDetail({ current, requestedWorkspaceKey: 'acme', viewerId: 'viewer-1', loadedRequestKey: fullKey })).toBe(false)
  })

  it('does not reuse a snapshot no finished full bootstrap produced', () => {
    const issue = { current, requestedWorkspaceKey: 'acme', viewerId: 'viewer-1', projection: 'issue-detail' as const }
    // Hydrated from the navigation cache, the page's own bootstrap still in flight.
    expect(canReuseWorkspaceForIssueDetail({ ...issue, loadedRequestKey: '' })).toBe(false)
    expect(canReuseWorkspaceForIssueDetail(issue)).toBe(false)
    // The project-list projection empties states, projects and templates.
    expect(canReuseWorkspaceForIssueDetail({ ...issue, loadedRequestKey: bootstrapRequestKey('viewer-1', 'acme', 'project-list') })).toBe(false)
    // Another workspace or viewer finished last.
    expect(canReuseWorkspaceForIssueDetail({ ...issue, loadedRequestKey: bootstrapRequestKey('viewer-1', 'other') })).toBe(false)
    expect(canReuseWorkspaceForIssueDetail({ ...issue, loadedRequestKey: bootstrapRequestKey('viewer-2', 'acme') })).toBe(false)
  })
})

/** Replays the bootstrap effect: the loaded key only moves when a fetch finishes. */
function navigate(routes: Array<BootstrapProjection | undefined>, start?: { data: typeof current | null; loaded: string }) {
  let data = start?.data ?? null
  let loaded = start?.loaded ?? ''
  return routes.map(projection => {
    const decision = bootstrapNavigationDecision({ current: data, requestedWorkspaceKey: 'acme', viewerId: 'viewer-1', projection, loadedRequestKey: loaded, inFlightKey: null })
    if (decision === 'fetch') {
      loaded = bootstrapRequestKey('viewer-1', 'acme', projection)
      data = { ...current, resourceDetailsOmitted: projection === 'issue-detail' }
    }
    return decision
  })
}

describe('bootstrapNavigationDecision', () => {
  it('project page -> issue -> back to project loads once', () => {
    expect(navigate([undefined, 'issue-detail', undefined])).toEqual(['fetch', 'reuse', 'skip'])
  })

  it('list page -> issue -> another page loads once, and an issue -> issue hop reuses again', () => {
    expect(navigate([undefined, 'issue-detail', 'issue-detail', undefined])).toEqual(['fetch', 'reuse', 'reuse', 'skip'])
  })

  it('cold load of an issue by URL fetches the issue projection, then the full one for other pages', () => {
    expect(navigate(['issue-detail', 'issue-detail', undefined, 'issue-detail', undefined])).toEqual(['fetch', 'skip', 'fetch', 'reuse', 'skip'])
  })

  it('a different projection than the reused snapshot satisfies still loads', () => {
    // Full snapshot -> issue -> project list needs the project-list projection.
    expect(navigate([undefined, 'issue-detail', 'project-list'])).toEqual(['fetch', 'reuse', 'fetch'])
    // Project list snapshot -> issue is not reused (states and projects are empty) -> back to the list reloads it.
    expect(navigate(['project-list', 'issue-detail', 'project-list'])).toEqual(['fetch', 'fetch', 'fetch'])
    // Back on a full page after that issue-detail snapshot.
    expect(navigate(['project-list', 'issue-detail', undefined])).toEqual(['fetch', 'fetch', 'fetch'])
  })

  it('does not reuse a cache-hydrated snapshot whose own bootstrap has not finished', () => {
    const hydrated = { data: current, loaded: '' }
    expect(navigate(['issue-detail'], hydrated)).toEqual(['fetch'])
  })

  it('keeps sharing an in-flight request for the same key', () => {
    expect(bootstrapNavigationDecision({ current, requestedWorkspaceKey: 'acme', viewerId: 'viewer-1', loadedRequestKey: '', inFlightKey: fullKey })).toBe('fetch')
  })
})

describe('bootstrapRefreshProjection', () => {
  const base = { viewerId: 'viewer-1', workspaceKey: 'acme' }

  it('refetches the full projection while an issue sits on a reused full snapshot', () => {
    expect(bootstrapRefreshProjection({ ...base, routeProjection: 'issue-detail', loadedRequestKey: fullKey })).toBeUndefined()
  })

  it('keeps the route projection otherwise', () => {
    expect(bootstrapRefreshProjection({ ...base, routeProjection: 'issue-detail', loadedRequestKey: bootstrapRequestKey('viewer-1', 'acme', 'issue-detail') })).toBe('issue-detail')
    expect(bootstrapRefreshProjection({ ...base, routeProjection: 'issue-detail', loadedRequestKey: '' })).toBe('issue-detail')
    expect(bootstrapRefreshProjection({ ...base, routeProjection: 'project-list', loadedRequestKey: fullKey })).toBe('project-list')
    expect(bootstrapRefreshProjection({ ...base, loadedRequestKey: fullKey })).toBeUndefined()
  })
})
