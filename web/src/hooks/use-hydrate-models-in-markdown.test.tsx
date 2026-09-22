import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { makeBootstrap, project } from '@/test/fixtures'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import { createWorkspaceStore } from '@/store/workspace-store'
import type { AuthSession, AccountBootstrap } from '@/types/flow'
import {
  hydrateModelsInMarkdown,
  useHydrateModelsInMarkdown,
} from './use-hydrate-models-in-markdown'

const session = { user: makeBootstrap().viewer, memberships: [], expiresAt: '2099-01-01' } as unknown as AuthSession
const account = { viewer: makeBootstrap().viewer, workspaces: [], lastWorkspaceKey: 'workspace' } as unknown as AccountBootstrap

function wrapper(store = createWorkspaceStore(makeBootstrap())) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <WorkspaceStoreProvider account={account} data={store.data} session={session} store={store}>
        {children}
      </WorkspaceStoreProvider>
    )
  }
}

describe('useHydrateModelsInMarkdown (LS-0738)', () => {
  it('hydrates models referenced in markdown via the workspace store', async () => {
    const markdown = `Link [issue](/workspace/issue/TST-1/test) and [project](/workspace/project/${project.slugId}/overview)`
    const { result } = renderHook(() => useHydrateModelsInMarkdown(markdown), { wrapper: wrapper() })
    expect(result.current.refs).toHaveLength(2)
    await waitFor(() => expect(result.current.pending).toBe(false))
    await waitFor(() => expect(result.current.models.length).toBe(2))
    expect(result.current.models.map((m) => m.type).sort()).toEqual(['issue', 'project'])
  })

  it('is a no-op without a store provider', () => {
    const { result } = renderHook(() => useHydrateModelsInMarkdown('[x](/workspace/issue/TST-1)'))
    expect(result.current.refs).toHaveLength(1)
    expect(result.current.models).toEqual([])
  })

  it('hydrateModelsInMarkdown helper resolves identifiers', async () => {
    const store = createWorkspaceStore(makeBootstrap())
    const models = await hydrateModelsInMarkdown(store, 'See Issue:issue-1 and /workspace/issue/TST-1')
    expect(models.some((m) => m.type === 'issue')).toBe(true)
  })
})
