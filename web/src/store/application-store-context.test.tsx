import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { AuthSession, AccountBootstrap } from '@/types/flow'
import { WorkspaceStoreProvider } from './application-store-context'
import { useApplicationStore } from './use-application-store'
import { useStore, useWorkspaceStore } from './use-workspace-store'

const session = {
  user: viewer,
  memberships: [],
  expiresAt: '2099-01-01T00:00:00.000Z',
} as unknown as AuthSession

const account = {
  viewer: viewer,
  workspaces: [],
  lastWorkspaceKey: 'workspace',
} as unknown as AccountBootstrap

function EntityProbe() {
  const store = useWorkspaceStore()
  const alias = useStore()
  return (
    <div>
      <span data-testid="issue-title">{(store.getById('issue', 'issue-1') as { title?: string } | undefined)?.title}</span>
      <span data-testid="alias-same">{String(alias === store)}</span>
    </div>
  )
}

function ApplicationProbe() {
  const app = useApplicationStore()
  return (
    <div>
      <span data-testid="viewer">{app.data?.viewer.id}</span>
      <span data-testid="session">{app.session?.user.id}</span>
      <span data-testid="account">{app.account?.viewer.id}</span>
    </div>
  )
}

describe('ApplicationStoreContext (LS-0063 / LS-0724 / LS-0770)', () => {
  it('provides dual contexts for entity store and application shell', () => {
    render(
      <WorkspaceStoreProvider data={makeBootstrap()} session={session} account={account}>
        <EntityProbe />
        <ApplicationProbe />
      </WorkspaceStoreProvider>,
    )
    expect(screen.getByTestId('issue-title').textContent).toBe('Test issue')
    expect(screen.getByTestId('alias-same').textContent).toBe('true')
    expect(screen.getByTestId('viewer').textContent).toBe(viewer.id)
    expect(screen.getByTestId('session').textContent).toBe(viewer.id)
    expect(screen.getByTestId('account').textContent).toBe(viewer.id)
  })

  it('throws outside the provider', () => {
    expect(() => render(<EntityProbe />)).toThrow(/WorkspaceStoreProvider/)
  })
})
