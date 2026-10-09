import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MentionShell, pasteText, stubEditorEnvironment } from '@/test/mention-host-harness'
import type { Cycle } from '@/types/flow'

const api = vi.hoisted(() => ({ fetchIssueRecord: vi.fn(), listProjectRecords: vi.fn(), listIssueRecords: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), ...api }))

import { resetAgentRecordCache } from '@/components/agent/agent-entity-fetch'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
import { CycleActions } from './cycle-menus'

beforeEach(() => {
  for (const mock of [api.fetchIssueRecord, api.listProjectRecords, api.listIssueRecords]) mock.mockReset()
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
  resetAgentRecordCache()
  stubEditorEnvironment()
})
afterEach(() => { vi.unstubAllGlobals() })

async function openEditor(user: ReturnType<typeof userEvent.setup>, cycle: Cycle, onUpdate: (input: unknown) => Promise<unknown>) {
  const data = mentionFixture()
  render(<MentionShell data={data}><CycleActions cycle={cycle} onReload={async () => undefined} onUpdate={onUpdate}/></MentionShell>)
  await user.click(screen.getByRole('button', { name: 'Open menu' }))
  await user.click(await screen.findByRole('menuitem', { name: /Edit cycle name and description/ }))
  return screen.findByRole('dialog')
}

describe('cycle edit dialog mentions', () => {
  it('saves a cycle description that mentions a document and a pasted Flow URL', async () => {
    const user = userEvent.setup()
    const cycle = mentionFixture().cycles[0] as Cycle
    const onUpdate = vi.fn().mockResolvedValue(undefined)
    const dialog = await openEditor(user, cycle, onUpdate)
    const box = within(dialog).getByRole('textbox', { name: 'Cycle description' })
    await user.click(box)
    // The dialog is modal (the page behind it is inert), so the "@" option is picked with the keyboard.
    await user.keyboard('Scope: @Launch')
    await screen.findByRole('option', { name: /Launch plan/ })
    await user.keyboard('{Enter}')
    pasteText(box, `${window.location.origin}${mentionUrls.project}`)
    await waitFor(() => expect(box.querySelector('a[data-agent-entity="project"]')).toHaveTextContent('Project one'))
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(onUpdate).toHaveBeenCalled())
    const input = onUpdate.mock.calls[0][0] as { description: string }
    expect(input.description).toContain('[Launch plan](/workspace/document/plan-abc)')
    expect(input.description).toContain(`[Project one](${mentionUrls.project})`)
  })

  it('shows the saved mention as a chip when the dialog is opened again', async () => {
    const user = userEvent.setup()
    const cycle = { ...mentionFixture().cycles[0], description: 'Plan: [Launch plan](/workspace/document/plan-abc)' } as Cycle
    const dialog = await openEditor(user, cycle, vi.fn().mockResolvedValue(undefined))
    const box = within(dialog).getByRole('textbox', { name: 'Cycle description' })
    await waitFor(() => expect(box.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
  })
})
