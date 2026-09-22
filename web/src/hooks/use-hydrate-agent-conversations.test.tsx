import { describe, expect, it, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useHydrateAgentConversations } from './use-hydrate-agent-conversations'

vi.mock('@/lib/api', () => ({
  getAgentSession: vi.fn(async (id: string) => ({
    id,
    slugId: id,
    userId: 'user',
    title: `Chat ${id}`,
    favorite: false,
    location: 'page' as const,
    issueIds: [],
    skillIds: [],
    messages: [],
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
  })),
  listAgentSessions: vi.fn(async () => []),
}))

describe('LS-0735 useHydrateAgentConversations', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('hydrates multiple conversation ids', async () => {
    const { result } = renderHook(() => useHydrateAgentConversations(['a', 'b']))
    await waitFor(() => expect(result.current.loading).toBe(false))
    expect(result.current.conversations.map(item => item.id)).toEqual(['a', 'b'])
    expect(result.current.hydrationRevision).toBeGreaterThanOrEqual(0)
  })
})
