import { act, renderHook } from '@testing-library/react'
import { describe, expect, it, beforeEach } from 'vitest'
import { ClientStorage } from '@/lib/client-storage'
import { clearViewPreferences, ViewPreferencesOrganization } from '@/lib/view-preferences'
import { getViewPreferences, useViewPreferences } from './use-view-preferences'
import { applyViewPreferencesFromSearchParams } from './use-view-preferences-from-query-params'

describe('useViewPreferences (LS-0781)', () => {
  beforeEach(() => {
    clearViewPreferences()
    ClientStorage.clear('local')
  })

  it('hydrates and persists preferences by view type', () => {
    const { result } = renderHook(() => useViewPreferences('issues', 'acme'))
    act(() => {
      result.current.setPreference('layout', 'board')
      result.current.setPreference('showSubIssues', false)
      result.current.save()
    })
    expect(result.current.preferences.layout).toBe('board')
    expect(result.current.preferences.showSubIssues).toBe(false)

    const again = renderHook(() => useViewPreferences('issues', 'acme'))
    expect(again.result.current.preferences.layout).toBe('board')
  })

  it('isolates preferences across view types', () => {
    const issues = getViewPreferences('acme', 'issues')
    const projects = getViewPreferences('acme', 'projects')
    issues.setPreference('layout', 'list')
    projects.setPreference('projectLayout', 'timeline')
    expect(issues.get().layout).toBe('list')
    expect(projects.get().projectLayout).toBe('timeline')
    expect(issues.get().projectLayout).toBeUndefined()
    expect(new ViewPreferencesOrganization('acme').hydrate().issues?.layout).toBe('list')
  })
})

describe('useViewPreferencesFromQueryParams helpers (LS-0782)', () => {
  beforeEach(() => {
    clearViewPreferences()
    ClientStorage.clear('local')
  })

  it('applies issue layout/ordering/grouping/show* from search params', () => {
    const handle = getViewPreferences('acme', 'issues')
    const consumed = applyViewPreferencesFromSearchParams(
      handle,
      '?layout=board&ordering=priority&grouping=status&showSubIssues=false&junk=1',
      'issues',
    )
    expect(consumed).toEqual(['layout', 'ordering', 'grouping', 'showSubIssues'])
    expect(handle.get()).toMatchObject({
      layout: 'board',
      viewOrdering: 'priority',
      issueGrouping: 'status',
      showSubIssues: false,
    })
  })

  it('maps triage dateCreated ordering to startedTriage', () => {
    const handle = getViewPreferences('acme', 'triage')
    applyViewPreferencesFromSearchParams(handle, '?ordering=dateCreated', 'triage')
    expect(handle.get().triageViewOrdering).toBe('startedTriage')
  })

  it('applies project layout from query', () => {
    const handle = getViewPreferences('acme', 'projects')
    applyViewPreferencesFromSearchParams(handle, '?layout=timeline', 'projects')
    expect(handle.get().projectLayout).toBe('timeline')
  })
})
