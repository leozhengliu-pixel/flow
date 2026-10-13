import { render, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { viewer } from '@/test/fixtures'
import type { AuditLogEntry, Initiative } from '@/types/flow'
import { InitiativeHistoryRows } from './initiative-history-rows'

const api = vi.hoisted(() => ({ listInitiativeHistory: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<object>()), ...api }))

describe('initiative history rows', () => {
  it('names its creator and what Flow changed on behalf of a person, newest first', async () => {
    const agent = { id: 'builtin-flow-agent', type: 'agent', name: 'Flow', avatarUrl: '/favicon.svg', onBehalfOfId: viewer.id }
    const history: AuditLogEntry[] = [
      { id: 'h1', actor: viewer, botActor: agent, action: 'created', resourceType: 'initiative', resourceId: 'i1', createdAt: '2026-10-12T09:00:00Z' },
      { id: 'h2', actor: viewer, botActor: agent, action: 'updated', resourceType: 'initiative', resourceId: 'i1', metadata: { changes: [{ field: 'status', from: 'active', to: 'planned' }] }, createdAt: '2026-10-12T09:01:00Z' },
    ]
    api.listInitiativeHistory.mockResolvedValue({ nodes: history, nextCursor: '', total: 2 })
    const initiative = { id: 'i1', name: 'QA', color: '#d15f64', creator: viewer, botActor: agent, createdAt: '2026-10-12T09:00:00Z', updatedAt: '2026-10-12T09:01:00Z' } as Initiative
    const { container } = render(<I18nProvider><InitiativeHistoryRows initiative={initiative} users={[viewer]}/></I18nProvider>)
    await waitFor(() => expect(container.querySelectorAll('.li-created-event')).toHaveLength(2))
    const rows = Array.from(container.querySelectorAll('.li-created-event'))
    expect(rows[0]).toHaveTextContent(`Flow changed the status to Planned on behalf of ${viewer.displayName}`)
    expect(rows[1]).toHaveTextContent(`Flow created the initiative on behalf of ${viewer.displayName}`)
  })
})
