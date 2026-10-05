import { describe, expect, it } from 'vitest'
import type { BootstrapData, PulseItem, SavedView } from '@/types/flow'
import { filterValues, nextPulseSeen, pulseConfigFromView, pulseFilterParam, pulseGroupKey, pulseGroupStarts, pulseLastSeenItemId, pulseViewMutation, weekStartsOnFromSetting } from './pulse-model'

function pulseData() {
  const viewer = { id: 'user-1', name: 'viewer', displayName: 'Viewer', active: true }
  const author = { id: 'user-2', name: 'author', displayName: 'Author', active: true }
  const status = { id: 'status-1', name: 'In progress', color: '#123456', type: 'started' }
  return {
    viewer, users: [viewer, author], teams: [{ id: 'team-1', name: 'Engineering' }],
    projects: [{ id: 'project-1', name: 'Project' }], initiatives: [{ id: 'initiative-1', name: 'Initiative' }], projectStatuses: [status],
    labels: [{ id: 'label-1', name: 'Portfolio', color: '#654321', resourceType: 'project' }],
    workspaceSettings: { featureFlags: {} },
  } as unknown as BootstrapData
}

const item = (id: string, createdAt: string) => ({ id, update: { createdAt } }) as unknown as PulseItem

describe('pulse model', () => {
  it('normalizes saved view configuration and serializes mutations', () => {
    const validFilter = { id: 'author', field: 'author', operator: 'is', values: ['user-1'] }
    const view = { filters: [validFilter, { id: 4 }], display: { match: 'any' } } as unknown as SavedView
    const config = pulseConfigFromView(view)
    expect(config).toEqual({ filters: [validFilter], match: 'any' })
    expect(pulseConfigFromView()).toEqual({ filters: [], match: 'all' })
    expect(pulseConfigFromView({ filters: {}, display: {} } as unknown as SavedView)).toEqual({ filters: [], match: 'all' })
    expect(pulseViewMutation(config)).toEqual({ filters: [validFilter], display: { match: 'any' } })
  })

  it('sends filters to the feed API as {match, filters} and drops empty ones', () => {
    expect(pulseFilterParam({ match: 'all', filters: [] })).toBeUndefined()
    expect(pulseFilterParam({ match: 'any', filters: [
      { id: 'a', field: 'health', operator: 'isNot', values: ['onTrack'] },
      { id: 'b', field: 'project', operator: 'is', values: [] },
    ] })).toEqual({ match: 'any', filters: [{ field: 'health', operator: 'isNot', values: ['onTrack'] }] })
  })

  it('builds picker values from workspace entities', () => {
    const data = pulseData()
    expect(filterValues(data, 'author')).toHaveLength(2)
    expect(filterValues(data, 'team')).toEqual([{ id: 'team-1', label: 'Engineering' }])
    expect(filterValues(data, 'projectStatus')).toEqual([{ id: 'status-1', label: 'In progress' }])
    expect(filterValues(data, 'projectStatusType').map(value => value.id)).toEqual(['started'])
    expect(filterValues(data, 'projectLabel')).toEqual([{ id: 'label-1', label: 'Portfolio' }])
    expect(filterValues(data, 'createdDate')).toHaveLength(4)
    expect(filterValues(data, 'health')).toHaveLength(4)
  })

  it('hides "Team update" unless team posts exist, even with the feedPostUpdate flag', () => {
    const data = pulseData()
    expect(filterValues(data, 'updateType').map(value => value.id)).toEqual(['project', 'initiative'])
    data.workspaceSettings = { featureFlags: { feedPostUpdate: true } } as unknown as BootstrapData['workspaceSettings']
    expect(filterValues(data, 'updateType').map(value => value.id)).toEqual(['project', 'initiative'])
  })

  it('groups items into Today / This week / Last week / This month / Last month / Older', () => {
    const now = new Date(2026, 9, 22, 15) // Thursday 22 Oct 2026
    expect(pulseGroupKey(new Date(2026, 9, 22, 8).toISOString(), now)).toBe('today')
    expect(pulseGroupKey(new Date(2026, 9, 19, 9).toISOString(), now)).toBe('thisWeek') // Monday
    expect(pulseGroupKey(new Date(2026, 9, 18, 9).toISOString(), now)).toBe('lastWeek') // Sunday
    expect(pulseGroupKey(new Date(2026, 9, 3, 9).toISOString(), now)).toBe('thisMonth')
    expect(pulseGroupKey(new Date(2026, 8, 3, 9).toISOString(), now)).toBe('lastMonth')
    expect(pulseGroupKey(new Date(2026, 5, 3, 9).toISOString(), now)).toBe('older')
    // Weeks starting on Sunday move Sunday into this week.
    expect(pulseGroupKey(new Date(2026, 9, 18, 9).toISOString(), now, weekStartsOnFromSetting('sunday'))).toBe('thisWeek')
    const items = [item('a', new Date(2026, 9, 22, 9).toISOString()), item('b', new Date(2026, 9, 22, 8).toISOString()), item('c', new Date(2026, 9, 20).toISOString()), item('d', new Date(2026, 5, 1).toISOString())]
    expect([...pulseGroupStarts(items, now).entries()]).toEqual([[0, 'today'], [2, 'thisWeek'], [3, 'older']])
  })

  it('puts the last-seen divider above the first item at or before the stable last-seen time', () => {
    const items = [item('new', '2026-10-03T10:00:00Z'), item('seen', '2026-10-02T10:00:00Z'), item('older', '2026-10-01T10:00:00Z')]
    expect(pulseLastSeenItemId(items, Date.parse('2026-10-02T12:00:00Z'))).toBe('seen')
    // Nothing new → no divider; never seen → no divider.
    expect(pulseLastSeenItemId(items, Date.parse('2026-10-04T00:00:00Z'))).toBeUndefined()
    expect(pulseLastSeenItemId(items, 0)).toBeUndefined()
  })

  it('only moves last seen forward', () => {
    expect(nextPulseSeen(undefined, '2026-10-01T00:00:00Z')).toBe(Date.parse('2026-10-01T00:00:00Z'))
    expect(nextPulseSeen('2026-10-02T00:00:00Z', '2026-10-01T00:00:00Z')).toBeUndefined()
    expect(nextPulseSeen(Date.parse('2026-10-02T00:00:00Z'), '2026-10-02T00:00:00Z')).toBeUndefined()
    expect(nextPulseSeen('2026-10-02T00:00:00Z', Date.parse('2026-10-03T00:00:00Z'))).toBe(Date.parse('2026-10-03T00:00:00Z'))
    expect(nextPulseSeen('2026-10-02T00:00:00Z', 'not a date')).toBeUndefined()
  })
})
