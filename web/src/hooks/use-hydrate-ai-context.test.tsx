import { renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { makeBootstrap } from '@/test/fixtures'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { createWorkspaceStore } from '@/store/workspace-store'
import type { AuthSession, AccountBootstrap } from '@/types/flow'
import { hydrateAiContext, useHydrateAiContext } from './use-hydrate-ai-context'

const data = makeBootstrap()
const session = { user: data.viewer, memberships: [], expiresAt: '2099-01-01' } as unknown as AuthSession
const account = { viewer: data.viewer, workspaces: [], lastWorkspaceKey: 'workspace' } as unknown as AccountBootstrap

function wrapper(store = createWorkspaceStore(data)) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <WorkspaceStoreProvider account={account} data={store.data} session={session} store={store}>
        {children}
      </WorkspaceStoreProvider>
    )
  }
}

describe('useHydrateAiContext (LS-0736)', () => {
  it('batch-hydrates type/id items through the store', async () => {
    const { result } = renderHook(() => useHydrateAiContext(), { wrapper: wrapper() })
    const hydrated = await result.current([
      { type: 'issue', id: 'issue-1' },
      { type: 'Issue', id: 'TST-1' },
      { type: 'project', id: 'project-1' },
      { type: 'issue', id: 'issue-1' },
    ])
    // Dedupes identical type:id; keeps identifier + uuid as distinct keys until hydrate.
    expect(hydrated).toHaveLength(3)
    expect(hydrated.filter((item) => item.type === 'issue').every((item) => item.model)).toBe(true)
    expect(hydrated.find((item) => item.type === 'project')?.model).toMatchObject({ id: 'project-1' })
  })

  it('returns empty when store is missing', async () => {
    const { result } = renderHook(() => useHydrateAiContext())
    await expect(result.current([{ type: 'issue', id: 'issue-1' }])).resolves.toEqual([])
  })

  it('hydrateAiContext helper resolves by identifier', async () => {
    const store = createWorkspaceStore(data)
    const hydrated = await hydrateAiContext(store, [{ type: 'issue', id: 'TST-1' }])
    expect(hydrated[0]?.model).toMatchObject({ identifier: 'TST-1' })
  })
})
