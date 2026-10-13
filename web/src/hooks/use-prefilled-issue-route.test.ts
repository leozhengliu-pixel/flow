// @vitest-environment jsdom
import { renderHook } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { BootstrapData } from '@/types/flow'
import { parseAppRoute } from '@/lib/app-routes'
import { usePrefilledIssueRoute } from './use-prefilled-issue-route'

const data = {
  workspace: { urlKey: 'acme' },
  viewer: { id: 'me' },
  users: [{ id: 'me', name: 'Me', displayName: 'me', email: 'me@x.test', active: true }],
  teams: [{ id: 't1', key: 'ENG', name: 'Eng' }],
  states: [], labels: [], labelGroups: [], cycles: [], projects: [], issues: [], issueTemplates: [], teamSettings: {},
} as unknown as BootstrapData

function mount(path: string, search: string, loaded = true) {
  const openCreateIssue = vi.fn()
  const navigateTo = vi.fn()
  const route = parseAppRoute(path, search)
  const props = { route, data: loaded ? data : null, location: { key: 'k1', search }, navigateTo, openCreateIssue }
  const view = renderHook(p => usePrefilledIssueRoute(p), { initialProps: props })
  return { openCreateIssue, navigateTo, view, props }
}

describe('usePrefilledIssueRoute', () => {
  it('opens the composer prefilled once and replaces the URL with the team issues page', () => {
    const { openCreateIssue, navigateTo, view, props } = mount('/acme/team/ENG/new', '?title=Hi&assignee=me')
    expect(openCreateIssue).toHaveBeenCalledWith({ teamId: 't1', title: 'Hi', assigneeId: 'me' })
    expect(navigateTo).toHaveBeenCalledWith('/acme/team/ENG/active', { replace: true })
    view.rerender({ ...props })
    expect(openCreateIssue).toHaveBeenCalledTimes(1)
  })

  it('returns to the workspace home when no team is in the path', () => {
    const { navigateTo } = mount('/acme/new', '?title=x')
    expect(navigateTo).toHaveBeenCalledWith('/acme', { replace: true })
  })

  it('waits for workspace data and ignores other routes', () => {
    expect(mount('/acme/new', '', false).openCreateIssue).not.toHaveBeenCalled()
    expect(mount('/acme/inbox', '').openCreateIssue).not.toHaveBeenCalled()
  })
})
