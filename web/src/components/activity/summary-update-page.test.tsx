import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { BootstrapData } from '@/types/flow'
import { SummaryUpdatePage, resolveSummaryUpdate, summaryUpdatePath } from './summary-update-page'

function fixture(): BootstrapData {
  const user = {
    id: 'u1',
    name: 'Ada',
    displayName: 'Ada Lovelace',
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
      memberIds: [],
      labelIds: [],
      initiatives: [],
      color: '#5e6ad2',
      icon: 'Project',
    }],
    initiatives: [],
    projectUpdates: {
      p1: [{
        id: 'upd-1',
        projectId: 'p1',
        body: 'Shipped the feed primitives',
        health: 'onTrack',
        createdAt: '2026-09-20T10:00:00.000Z',
        user,
        comments: [],
        reactions: {},
        attachments: [],
      }],
    },
    initiativeUpdates: {},
    userSettings: {},
    savedViews: [],
    subscriptions: [],
    labels: [],
    projectStatuses: [],
  } as unknown as BootstrapData
}

describe('SummaryUpdatePage (LS-0570)', () => {
  it('resolves project updates by post id', () => {
    const data = fixture()
    const resolved = resolveSummaryUpdate(data, 'upd-1')
    expect(resolved?.kind).toBe('project')
    expect(resolved && 'projectName' in resolved ? resolved.projectName : '').toBe('Alpha')
  })

  it('builds summary update path', () => {
    expect(summaryUpdatePath('flow', 'upd-1')).toBe('/flow/update/upd-1')
  })

  it('renders UpdatePage chrome for a resolved post', () => {
    render(<SummaryUpdatePage data={fixture()} onNavigate={vi.fn()} postId="upd-1" />)
    expect(screen.getByText('Your Pulse')).toBeTruthy()
    expect(screen.getByText('Alpha')).toBeTruthy()
    expect(screen.getByText('Shipped the feed primitives')).toBeTruthy()
  })

  it('shows not-found when post is missing', () => {
    render(<SummaryUpdatePage data={fixture()} onNavigate={vi.fn()} postId="missing" />)
    expect(screen.getByText('Update not found')).toBeTruthy()
  })
})
