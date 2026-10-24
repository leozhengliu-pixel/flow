import { describe, expect, it } from 'vitest'
import { canReuseWorkspaceForIssueDetail } from './bootstrap-navigation'

const current = {
  workspace: { urlKey: 'acme' },
  viewer: { id: 'viewer-1' },
  resourceDetailsOmitted: false,
}

describe('canReuseWorkspaceForIssueDetail', () => {
  it('reuses a complete workspace snapshot for an issue opened in place', () => {
    expect(canReuseWorkspaceForIssueDetail({ current, requestedWorkspaceKey: 'acme', viewerId: 'viewer-1', projection: 'issue-detail' })).toBe(true)
  })

  it('requires the same viewer and a complete snapshot', () => {
    expect(canReuseWorkspaceForIssueDetail({ current, requestedWorkspaceKey: 'other', viewerId: 'viewer-1', projection: 'issue-detail' })).toBe(false)
    expect(canReuseWorkspaceForIssueDetail({ current, requestedWorkspaceKey: 'acme', viewerId: 'viewer-2', projection: 'issue-detail' })).toBe(false)
    expect(canReuseWorkspaceForIssueDetail({ current: { ...current, resourceDetailsOmitted: true }, requestedWorkspaceKey: 'acme', viewerId: 'viewer-1', projection: 'issue-detail' })).toBe(false)
    expect(canReuseWorkspaceForIssueDetail({ current, requestedWorkspaceKey: 'acme', viewerId: 'viewer-1' })).toBe(false)
  })
})
