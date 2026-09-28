import { describe, expect, it } from 'vitest'
import { createWorkspaceStore } from '@/store/workspace-store'
import type { BootstrapData, Issue } from '@/types/flow'
import { extractUrlsFromClipboard, resolveMentionFromStore } from './mention-hydration'

function issue(partial: Partial<Issue> & Pick<Issue, 'id' | 'identifier' | 'title'>): Issue {
  return {
    version: 1,
    number: 1,
    description: '',
    priority: 0,
    priorityLabel: 'No priority',
    sortOrder: 0,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    team: { id: 'team-1', name: 'Eng', key: 'ENG' } as Issue['team'],
    state: { id: 'state-1', name: 'Todo', type: 'unstarted', color: '#ccc', position: 1 } as Issue['state'],
    creator: { id: 'user-1', name: 'Ada', displayName: 'Ada', email: 'a@x', active: true } as Issue['creator'],
    labels: [],
    subscriberIds: [],
    reactions: {},
    ...partial,
  } as Issue
}


describe('MentionHydrationPlugin helpers (LS-0407)', () => {
  it('extracts absolute and relative URLs from clipboard text', () => {
    expect(extractUrlsFromClipboard('https://app.example/acme/issue/ENG-12/title')).toEqual([
      'https://app.example/acme/issue/ENG-12/title',
    ])
    expect(extractUrlsFromClipboard('/acme/issue/ENG-9/foo')).toEqual(['/acme/issue/ENG-9/foo'])
  })

  it('resolves issue URLs from the workspace store directory', () => {
    const data = {
      issues: [issue({ id: 'issue-1', identifier: 'ENG-12', title: 'Ship sync' })],
      users: [{ id: 'user-9', name: 'Ada', displayName: 'Ada Lovelace', email: 'ada@x', active: true }],
    } as unknown as BootstrapData
    const store = createWorkspaceStore(data)
    const resolved = resolveMentionFromStore('https://flow.local/acme/issue/ENG-12/ship-sync', store)
    expect(resolved).toEqual({
      id: 'issue-1',
      label: 'ENG-12',
      title: 'Ship sync',
      href: 'https://flow.local/acme/issue/ENG-12/ship-sync',
      mentionType: 'issue',
    })
    const user = resolveMentionFromStore('/acme/profiles/user-9', store)
    expect(user?.mentionType).toBe('user')
    expect(user?.id).toBe('user-9')
    expect(user?.label).toContain('Ada')
  })

  it('returns null when the store has no matching entity', () => {
    const store = createWorkspaceStore({ issues: [], users: [] } as unknown as BootstrapData)
    expect(resolveMentionFromStore('/acme/issue/ENG-99/x', store)).toBeNull()
  })
})
