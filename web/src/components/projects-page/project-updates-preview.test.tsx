import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MentionShell, pasteText, stubEditorEnvironment } from '@/test/mention-host-harness'
import type { ProjectUpdate } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), ...api }))

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { ProjectUpdatesPreview } from './project-updates-preview'

beforeEach(() => {
  for (const mock of [api.fetchIssueRecord, api.listProjectRecords, api.listIssueRecords]) mock.mockReset()
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  resetAgentRecordCache()
  stubEditorEnvironment()
})
afterEach(() => { vi.unstubAllGlobals() })

function Panel({ data, onClose, onComment, onCreate, updates = [] }: { data: ReturnType<typeof mentionFixture>; onClose: () => void; onComment?: (projectId: string, updateId: string, body: string) => Promise<ProjectUpdate>; onCreate?: (projectId: string, input: { body: string }) => Promise<ProjectUpdate>; updates?: ProjectUpdate[] }) {
  const [project] = useState(data.projects[0])
  return <MentionShell data={data}><ProjectUpdatesPreview onClose={onClose} onComment={onComment} onCreate={onCreate} project={project} updates={updates} viewer={data.users[0]}/></MentionShell>
}

describe('ProjectUpdatesPreview mentions', () => {
  it('posts a project update holding a mention and a pasted Flow URL without closing the panel', async () => {
    const user = userEvent.setup()
    const data = mentionFixture()
    const onClose = vi.fn()
    const onCreate = vi.fn().mockResolvedValue({})
    render(<Panel data={data} onClose={onClose} onCreate={onCreate}/>)

    await user.click(screen.getAllByRole('button', { name: 'Write project update' })[0])
    const box = await screen.findByRole('textbox', { name: 'Project update' })
    await waitFor(() => expect(box).toHaveFocus())
    await user.keyboard('Plan: @Launch')
    await user.click(await screen.findByRole('option', { name: /Launch plan/ }))
    expect(onClose).not.toHaveBeenCalled()
    pasteText(box, `${window.location.origin}${mentionUrls.initiative}`)
    await waitFor(() => expect(box.querySelector('a[data-agent-entity="initiative"]')).toHaveTextContent('Roadmap'))
    await user.click(screen.getByRole('button', { name: 'Post update' }))

    await waitFor(() => expect(onCreate).toHaveBeenCalled())
    const body = onCreate.mock.calls[0][1].body as string
    expect(body).toContain('[Launch plan](/workspace/document/plan-abc)')
    expect(body).toContain(`[Roadmap](${mentionUrls.initiative})`)
  })

  it('shows the mention chip when an update is edited again and comments with a mention', async () => {
    const user = userEvent.setup()
    const data = mentionFixture()
    const onComment = vi.fn().mockResolvedValue({})
    const update = { id: 'update-1', projectId: data.projects[0].id, body: 'See [Launch plan](/workspace/document/plan-abc)', health: 'onTrack', createdAt: new Date().toISOString(), user: data.users[0], comments: [], reactions: {} } as unknown as ProjectUpdate
    render(<Panel data={data} onClose={vi.fn()} onComment={onComment} updates={[update]}/>)

    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }))
    const box = await screen.findByRole('textbox', { name: 'Project update' })
    await waitFor(() => expect(box.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    await user.click(screen.getByRole('button', { name: '0 comments' }))
    const comment = await screen.findByRole('textbox', { name: 'Add comment' })
    await user.click(comment)
    await user.keyboard('Thanks @Road')
    await user.click(await screen.findByRole('option', { name: /Roadmap/ }))
    await user.keyboard('{Control>}{Enter}{/Control}')
    await waitFor(() => expect(onComment).toHaveBeenCalledWith(data.projects[0].id, 'update-1', 'Thanks [Roadmap](/workspace/initiative/roadmap/overview)'))
    await waitFor(() => expect(within(comment).queryByText('Roadmap')).toBeNull())
  })
})
