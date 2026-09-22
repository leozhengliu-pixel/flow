import { describe, expect, it } from 'vitest'
import type { BootstrapData } from '@/types/flow'
import { createFeedItemsListProvider, feedItemsLiveCount } from './feed-items-list-provider'

function fixture(): BootstrapData {
  const user = {
    id: 'u1',
    name: 'Ada',
    displayName: 'Ada',
    email: 'ada@example.com',
    avatarUrl: '',
    active: true,
    emailVerified: true,
  }
  return {
    workspace: { id: 'w1', name: 'Flow', urlKey: 'flow', organizationId: 'o1' },
    viewer: user,
    users: [user],
    teams: [],
    projects: [{
      id: 'p1',
      name: 'Alpha',
      slugId: 'alpha',
      status: { id: 's1', name: 'Started', type: 'started', color: '#000' },
      health: 'onTrack',
      teamIds: [],
      memberIds: [user.id],
      labelIds: [],
      initiatives: [],
      color: '#5e6ad2',
      icon: 'Project',
      lead: user,
    }],
    initiatives: [{
      id: 'i1',
      name: 'Beta',
      slugId: 'beta',
      status: 'active',
      owner: user,
      projectIds: [],
      contributingTeamIds: [],
      color: '#5e6ad2',
      icon: 'Initiative',
      subscribed: false,
    }],
    projectUpdates: {
      p1: [{
        id: 'pu1',
        projectId: 'p1',
        body: 'Project note',
        health: 'onTrack',
        createdAt: '2026-09-21T10:00:00.000Z',
        user,
        comments: [],
        reactions: {},
        attachments: [],
      }],
    },
    initiativeUpdates: {
      i1: [{
        id: 'iu1',
        initiativeId: 'i1',
        body: 'Initiative note',
        health: 'atRisk',
        createdAt: '2026-09-20T10:00:00.000Z',
        user,
        comments: [],
        reactions: {},
        attachments: [],
      }],
    },
    userSettings: {},
    savedViews: [],
    subscriptions: [],
    labels: [],
    projectStatuses: [],
    workspaceSettings: { featureFlags: {} },
  } as unknown as BootstrapData
}

describe('FeedItemsListProvider (LS-0267)', () => {
  it('exposes prefiltered and filtered item APIs', () => {
    const data = fixture()
    const provider = createFeedItemsListProvider({ data, view: 'all' })
    expect(provider.name).toBe('FeedItemsListProvider')
    expect(provider.modelType).toBe('feedItem')
    expect(provider.prefilteredItems).toHaveLength(2)
    expect(provider.filteredItems).toHaveLength(2)
  })

  it('applies facet filters for live counts', () => {
    const data = fixture()
    const provider = createFeedItemsListProvider({
      data,
      view: 'all',
      filters: [{ id: 'f1', field: 'updateType', operator: 'is', values: ['project'] }],
      match: 'all',
    })
    expect(feedItemsLiveCount(provider)).toEqual({ prefiltered: 2, filtered: 1 })
    expect(provider.filteredItems.map(item => item.kind)).toEqual(['project'])
  })
})
