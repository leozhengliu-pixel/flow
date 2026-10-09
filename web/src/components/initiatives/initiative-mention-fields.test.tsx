import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ComponentProps } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { MentionShell, pasteText, stubEditorEnvironment } from '@/test/mention-host-harness'
import type { Initiative } from '@/types/flow'

const api = vi.hoisted(() => ({
  fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn(),
  createDraft: vi.fn(), updateDraft: vi.fn(), deleteDraft: vi.fn(),
  realtimeClientId: () => 'initiative-field-test',
}))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<object>()), ...api }))

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { EditableArea, InitiativeActivity, InlineCommentEditor, InlineUpdateEditor } from './initiative-detail-page'
import { UpdatesFloatingPanel } from './updates-floating-panel'

const data = mentionFixture()
const initiative = data.initiatives[0] as Initiative
const viewer = data.users[0]
const activityProps = (overrides: object) => ({ initiative, initiativeUpdates: [], viewer, users: data.users, display: { updates: true, comments: true, activity: true }, onCreateUpdate: vi.fn(), onComment: vi.fn(), onUpdateInitiativeUpdate: vi.fn(), onDeleteUpdate: vi.fn(), ...overrides }) as unknown as ComponentProps<typeof InitiativeActivity>
const documentLink = '[Launch plan](/workspace/document/plan-abc)'
const projectLink = '[Project one](/workspace/project/project-one/overview)'

beforeEach(() => {
  for (const mock of [api.fetchIssueRecord, api.listProjectRecords, api.listIssueRecords, api.createDraft, api.updateDraft, api.deleteDraft]) mock.mockReset()
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  api.createDraft.mockResolvedValue({ id: 'draft-1', updatedAt: '2026-10-01T00:00:00Z' })
  api.updateDraft.mockResolvedValue({ id: 'draft-1', updatedAt: '2026-10-01T00:00:00Z' })
  api.deleteDraft.mockResolvedValue(undefined)
  localStorage.clear()
  resetAgentRecordCache()
  stubEditorEnvironment()
})
afterEach(() => { vi.unstubAllGlobals() })

/** Types "See @Launch", picks the document, pastes a project URL and waits for both chips. */
async function mention(user: ReturnType<typeof userEvent.setup>, name: string) {
  const box = await screen.findByRole('textbox', { name })
  await user.click(box)
  await user.keyboard('See @Launch')
  await user.click(await screen.findByRole('option', { name: /Launch plan/ }))
  pasteText(box, `${window.location.origin}${mentionUrls.project}`)
  await waitFor(() => expect(document.querySelector('a[data-agent-entity="project"]')).toHaveTextContent('Project one'))
  expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan')
}

describe('initiative activity composer', () => {
  it('posts an update with mentions as markdown links', async () => {
    const user = userEvent.setup()
    const onCreateUpdate = vi.fn().mockResolvedValue({})
    render(<MentionShell data={data}><InitiativeActivity {...activityProps({ onCreateUpdate })}/></MentionShell>)
    await mention(user, 'Initiative update')
    await user.click(screen.getByRole('button', { name: 'Post update' }))
    await waitFor(() => expect(onCreateUpdate).toHaveBeenCalledTimes(1))
    const body = onCreateUpdate.mock.calls[0][1].body as string
    expect(body).toContain(documentLink)
    expect(body).toContain(projectLink)
    await waitFor(() => expect(screen.getByRole('textbox', { name: 'Initiative update' })).not.toHaveTextContent('See'))
  })

  it('posts a comment with Cmd/Ctrl+Enter', async () => {
    const user = userEvent.setup()
    const onComment = vi.fn().mockResolvedValue(undefined)
    render(<MentionShell data={data}><InitiativeActivity {...activityProps({ onComment })}/></MentionShell>)
    await user.click(screen.getByRole('tab', { name: 'Comment' }))
    await user.click(await screen.findByRole('textbox', { name: 'Initiative comment' }))
    await user.keyboard('Nice{Control>}{Enter}{/Control}')
    await waitFor(() => expect(onComment).toHaveBeenCalledWith(initiative.id, 'Nice'))
  })
})

describe('initiative update and comment editors', () => {
  it('saves mentions of an edited update and shows the chips again when it is edited later', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn().mockResolvedValue(undefined)
    const update = { id: 'update-1', body: 'Old', health: 'onTrack', createdAt: '', user: viewer } as never
    const first = render(<MentionShell data={data}><InlineUpdateEditor update={update} onCancel={vi.fn()} onSave={onSave}/></MentionShell>)
    await mention(user, 'Edit initiative update')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
    const body = onSave.mock.calls[0][0].body as string
    expect(body).toContain(documentLink)
    expect(body).toContain(projectLink)
    first.unmount()
    render(<MentionShell data={data}><InlineUpdateEditor update={{ ...(update as object), body } as never} onCancel={vi.fn()} onSave={onSave}/></MentionShell>)
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
  })

  it('saves mentions of an edited comment', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn().mockResolvedValue(undefined)
    const comment = { id: 'comment-1', body: 'Old', createdAt: '', user: viewer, reactions: [] } as never
    render(<MentionShell data={data}><InlineCommentEditor comment={comment} onCancel={vi.fn()} onSave={onSave}/></MentionShell>)
    await mention(user, 'Edit comment')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
    expect(onSave.mock.calls[0][0]).toContain(documentLink)
  })
})

describe('initiative description', () => {
  it('commits the markdown with mentions on blur and shows the chips when it is mounted again', async () => {
    const user = userEvent.setup()
    const onCommit = vi.fn()
    const first = render(<MentionShell data={data}><EditableArea label="Initiative description" placeholder="Add description…" value="" onCommit={onCommit}/><button type="button">Elsewhere</button></MentionShell>)
    await mention(user, 'Initiative description')
    await user.click(screen.getByRole('button', { name: 'Elsewhere' }))
    await waitFor(() => expect(onCommit).toHaveBeenCalledTimes(1))
    const saved = onCommit.mock.calls[0][0] as string
    expect(saved).toContain(documentLink)
    expect(saved).toContain(projectLink)
    first.unmount()
    render(<MentionShell data={data}><EditableArea label="Initiative description" value={saved} onCommit={onCommit}/></MentionShell>)
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
    expect(document.querySelector('a[data-agent-entity="project"]')).toHaveTextContent('Project one')
  })
})

describe('updates floating panel', () => {
  it('posts a new update with mentions as markdown links, and lists posted updates with chips', async () => {
    const user = userEvent.setup()
    const onCreateUpdate = vi.fn().mockResolvedValue({})
    const props = { initiative, viewer, open: true, onClose: vi.fn(), onOpenActivity: vi.fn(), onCreateUpdate, onUpdate: vi.fn() }
    const first = render(<MentionShell data={data}><UpdatesFloatingPanel {...props} updates={[]}/></MentionShell>)
    await user.click(screen.getAllByRole('button', { name: 'New update' })[0])
    await mention(user, 'Initiative update')
    await user.click(screen.getByRole('button', { name: 'Post update' }))
    await waitFor(() => expect(onCreateUpdate).toHaveBeenCalledTimes(1))
    const body = onCreateUpdate.mock.calls[0][1].body as string
    expect(body).toContain(documentLink)
    first.unmount()
    render(<MentionShell data={data}><UpdatesFloatingPanel {...props} updates={[{ id: 'update-1', body, health: 'onTrack', createdAt: '2026-10-01T00:00:00Z', user: viewer } as never]}/></MentionShell>)
    await waitFor(() => expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
  })
})
