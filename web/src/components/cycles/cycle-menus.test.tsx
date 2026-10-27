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

describe('cycle edit dialog capacity', () => {
  it('shows the automatic capacity, saves a typed override and leaves capacity out when untouched', async () => {
    const user = userEvent.setup()
    const cycle = { ...mentionFixture().cycles[0], capacity: 8, capacityAutomatic: 8, capacitySource: 'velocity', capacityOverride: undefined } as Cycle
    const onUpdate = vi.fn().mockResolvedValue(undefined)
    let dialog = await openEditor(user, cycle, onUpdate)
    const field = within(dialog).getByRole('spinbutton', { name: /Cycle capacity/ })
    expect(field).toHaveValue(null)
    expect(field).toHaveAttribute('placeholder', 'Automatic (8 points)')
    expect(dialog).toHaveTextContent("Average completed in the team's last completed cycles (up to three).")
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(1))
    expect(onUpdate.mock.calls[0][0]).not.toHaveProperty('capacity')

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: /Edit cycle name and description/ }))
    dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('spinbutton', { name: /Cycle capacity/ }), '12')
    expect(dialog).toHaveTextContent('Automatic (8 points)')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onUpdate).toHaveBeenCalledTimes(2))
    expect(onUpdate.mock.calls[1][0]).toMatchObject({ capacity: 12 })
  })

  it('returns an override to automatic with "Use automatic"', async () => {
    const user = userEvent.setup()
    const cycle = { ...mentionFixture().cycles[0], capacity: 20, capacityAutomatic: 30, capacitySource: 'override', capacityOverride: 20 } as Cycle
    const onUpdate = vi.fn().mockResolvedValue(undefined)
    const dialog = await openEditor(user, cycle, onUpdate)
    expect(within(dialog).getByRole('spinbutton', { name: /Cycle capacity/ })).toHaveValue(20)
    expect(dialog).toHaveTextContent('Automatic (30 points)')
    await user.click(within(dialog).getByRole('button', { name: 'Use automatic' }))
    expect(within(dialog).getByRole('spinbutton', { name: /Cycle capacity/ })).toHaveAttribute('placeholder', 'Automatic (30 points)')
    expect(dialog).toHaveTextContent('Saving returns the cycle to the automatic capacity.')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onUpdate).toHaveBeenCalled())
    expect(onUpdate.mock.calls[0][0]).toMatchObject({ capacity: null })
  })
})
