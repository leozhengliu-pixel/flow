import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { MentionShell, pasteText, stubEditorEnvironment } from '@/test/mention-host-harness'
import type { User } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn(), realtimeClientId: () => 'base-update-test' }))
vi.mock('@/lib/api', () => api)

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { BaseUpdateInboxView } from './base-update-inbox-view'

const viewer = { id: 'user-1', name: 'Viewer', displayName: 'Viewer', email: 'viewer@flow.test', active: true } as User

beforeEach(() => {
  for (const mock of [api.fetchIssueRecord, api.listProjectRecords, api.listIssueRecords]) mock.mockReset()
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  resetAgentRecordCache()
  stubEditorEnvironment()
})
afterEach(() => { vi.unstubAllGlobals() })

const view = (props: Partial<Parameters<typeof BaseUpdateInboxView>[0]> = {}) => (
  <MentionShell data={mentionFixture()}>
    <BaseUpdateInboxView entityColor="var(--theme-text-primary)" entityKind="project" entityName="Launch" onCreateUpdate={vi.fn()} onOpenEntity={vi.fn()} surfaceId="LS-0099" updates={[]} viewer={viewer} {...props}/>
  </MentionShell>
)

describe('update composer in the inbox', () => {
  it('posts a mention as a markdown link, turns a pasted Flow URL into a chip, and shows the chip when the update is listed', async () => {
    const user = userEvent.setup()
    const onCreateUpdate = vi.fn().mockResolvedValue(undefined)
    const first = render(view({ onCreateUpdate }))
    const box = await screen.findByRole('textbox', { name: 'Project update' })
    await user.click(box)
    await user.keyboard('Plan @Launch')
    await user.click(await screen.findByRole('option', { name: /Launch plan/ }))
    pasteText(box, `${window.location.origin}${mentionUrls.project}`)
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="project"]')).toHaveTextContent('Project one'))
    await user.click(screen.getByRole('button', { name: 'Post update' }))
    await waitFor(() => expect(onCreateUpdate).toHaveBeenCalledTimes(1))
    const saved = onCreateUpdate.mock.calls[0][0].body as string
    expect(saved).toContain('[Launch plan](/workspace/document/plan-abc)')
    expect(saved).toContain('[Project one](/workspace/project/project-one/overview)')
    first.unmount()
    render(view({ updates: [{ id: 'update-1', body: saved, health: 'onTrack', createdAt: '2026-10-01T00:00:00Z', user: viewer }] }))
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
  })

  it('submits with Cmd/Ctrl+Enter', async () => {
    const user = userEvent.setup()
    const onCreateUpdate = vi.fn().mockResolvedValue(undefined)
    render(view({ onCreateUpdate }))
    await user.click(await screen.findByRole('textbox', { name: 'Project update' }))
    await user.keyboard('Shipped{Control>}{Enter}{/Control}')
    await waitFor(() => expect(onCreateUpdate).toHaveBeenCalledWith({ body: 'Shipped', health: 'onTrack' }))
  })
})
