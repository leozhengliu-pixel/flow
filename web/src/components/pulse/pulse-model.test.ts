import { describe, expect, it, vi } from 'vitest'
import type { BootstrapData, SavedView } from '@/types/flow'
import { buildPulseFeed, filterValues, pulseConfigFromView, pulseViewMutation } from './pulse-model'

function pulseData() {
  const viewer = { id: 'user-1', name: 'viewer', displayName: 'Viewer', active: true }
  const author = { id: 'user-2', name: 'author', displayName: 'Author', active: true }
  const status = { id: 'status-1', name: 'In progress', color: '#123456', type: 'started' }
  const project = {
    id: 'project-1', name: 'Project', teamIds: ['team-1'], memberIds: [viewer.id], labelIds: ['label-1'],
    initiatives: ['initiative-1'], status, lead: viewer,
  }
  const initiative = {
    id: 'initiative-1', name: 'Initiative', projectIds: [], contributingTeamIds: ['team-1'],
    leadTeamId: 'team-1', subscribed: false, owner: author,
  }
  const projectUpdate = {
    id: 'project-update-1', user: author, health: 'onTrack', createdAt: '2026-08-30T08:00:00.000Z',
    comments: [{ id: 'comment-1' }], reactions: { thumbsUp: [viewer.id] },
  }
  const initiativeUpdate = {
    id: 'initiative-update-1', user: viewer, health: 'atRisk', createdAt: '2026-08-29T08:00:00.000Z',
    comments: [], reactions: {},
  }
  return {
    viewer, users: [viewer, author], teams: [{ id: 'team-1', name: 'Engineering' }],
    projects: [project], initiatives: [initiative], projectStatuses: [status],
    labels: [{ id: 'label-1', name: 'Portfolio', color: '#654321', resourceType: 'project' }],
    subscriptions: [], projectUpdates: { [project.id]: [projectUpdate] },
    initiativeUpdates: { [initiative.id]: [initiativeUpdate] },
  } as unknown as BootstrapData
}

describe('pulse model', () => {
  it('sorts updates and applies following and filter semantics', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-30T12:00:00.000Z'))
    const data = pulseData()
    expect(buildPulseFeed(data, 'all').map(item => item.id)).toEqual(['project:project-update-1', 'initiative:initiative-update-1'])
    expect(buildPulseFeed(data, 'following').map(item => item.kind)).toEqual(['project'])
    expect(buildPulseFeed(data, 'popular')[0].kind).toBe('project')
    expect(buildPulseFeed(data, 'all', {
      match: 'all', filters: [{ id: 'type', field: 'updateType', operator: 'is', values: ['initiative'] }],
    }).map(item => item.kind)).toEqual(['initiative'])
    expect(buildPulseFeed(data, 'all', {
      match: 'any', filters: [
        { id: 'health', field: 'health', operator: 'isNot', values: ['onTrack'] },
        { id: 'project', field: 'project', operator: 'is', values: ['missing'] },
      ],
    }).map(item => item.kind)).toEqual(['initiative'])

    data.initiatives[0].projectIds = ['project-1']
    const filters = [
      { field: 'author', value: 'user-2', count: 1 },
      { field: 'team', value: 'team-1', count: 2 },
      { field: 'createdDate', value: 'past-day', count: 1 },
      { field: 'health', value: 'atRisk', count: 1 },
      { field: 'initiative', value: 'initiative-1', count: 2 },
      { field: 'project', value: 'project-1', count: 2 },
      { field: 'projectMember', value: 'user-1', count: 2 },
      { field: 'projectStatus', value: 'status-1', count: 2 },
      { field: 'projectLabel', value: 'label-1', count: 2 },
    ] as const
    for (const filter of filters) {
      expect(buildPulseFeed(data, 'all', {
        match: 'all', filters: [{ id: filter.field, field: filter.field, operator: 'is', values: [filter.value] }],
      }), filter.field).toHaveLength(filter.count)
    }
    vi.useRealTimers()
  })

  it('normalizes saved view configuration and serializes mutations', () => {
    const validFilter = { id: 'author', field: 'author', operator: 'is', values: ['user-1'] }
    const view = { filters: [validFilter, { id: 4 }], display: { match: 'any' } } as unknown as SavedView
    const config = pulseConfigFromView(view)
    expect(config).toEqual({ filters: [validFilter], match: 'any' })
    expect(pulseConfigFromView()).toEqual({ filters: [], match: 'all' })
    expect(pulseConfigFromView({ filters: {}, display: {} } as unknown as SavedView)).toEqual({ filters: [], match: 'all' })
    expect(pulseViewMutation(config)).toEqual({ filters: [validFilter], display: { match: 'any' } })
  })

  it('builds picker values from workspace entities', () => {
    const data = pulseData()
    expect(filterValues(data, 'author')).toHaveLength(2)
    expect(filterValues(data, 'team')).toEqual([{ id: 'team-1', label: 'Engineering' }])
    expect(filterValues(data, 'projectStatus')).toEqual([{ id: 'status-1', label: 'In progress' }])
    expect(filterValues(data, 'projectLabel')).toEqual([{ id: 'label-1', label: 'Portfolio' }])
    expect(filterValues(data, 'createdDate')).toHaveLength(4)
    expect(filterValues(data, 'updateType')).toHaveLength(2)
    expect(filterValues(data, 'health')).toHaveLength(4)
    expect(filterValues(data, 'initiative')).toEqual([{ id: 'initiative-1', label: 'Initiative' }])
    expect(filterValues(data, 'project')).toEqual([{ id: 'project-1', label: 'Project' }])
    expect(filterValues(data, 'projectMember')).toHaveLength(2)
  })
})
