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

describe('cycle date actions', () => {
  const scheduled = (id: string, number: number, status: Cycle['status'], start: string, end: string) => ({ ...mentionFixture().cycles[0], id, number, name: `Cycle ${number}`, status, startsAt: `${start}T00:00:00Z`, endsAt: `${end}T00:00:00Z` }) as Cycle
  const c1 = scheduled('c1', 1, 'current', '2026-10-28', '2026-11-15')
  const c2 = scheduled('c2', 2, 'upcoming', '2026-11-30', '2026-12-13')
  const c3 = scheduled('c3', 3, 'upcoming', '2026-12-28', '2027-01-10')
  beforeEach(() => { vi.useFakeTimers({ toFake: ['Date'] }); vi.setSystemTime(new Date('2026-10-28T08:00:00Z')) })
  afterEach(() => { vi.useRealTimers() })

  function renderActions(cycle: Cycle, cycles: Cycle[], handlers: { onUpdate?: (input: unknown) => Promise<unknown>; onStart?: () => Promise<unknown> } = {}) {
    render(<MentionShell data={mentionFixture()}><CycleActions cycle={cycle} cycles={cycles} onReload={async () => undefined} onUpdate={handlers.onUpdate ?? vi.fn().mockResolvedValue(undefined)} onStart={handlers.onStart ?? vi.fn().mockResolvedValue(undefined)}/></MentionShell>)
  }
  async function openDates(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    ;(await screen.findByRole('menuitem', { name: 'Change cycle dates' })).focus()
    await user.keyboard('{ArrowRight}')
    await screen.findByRole('menuitem', { name: 'Move end date…' })
  }
  // Submenu items are chosen with the keyboard, as pointer moves close a submenu in jsdom.
  async function choose(user: ReturnType<typeof userEvent.setup>, name: string) {
    screen.getByRole('menuitem', { name }).focus()
    await user.keyboard('{Enter}')
  }
  const dayButton = (year: number, month: number, date: number) => screen.getByRole('button', { name: new Date(year, month - 1, date).toLocaleDateString() })

  it('offers only "Move end date…" on the current cycle and no way to complete it', async () => {
    const user = userEvent.setup()
    renderActions(c1, [c1, c2, c3])
    await openDates(user)
    expect(screen.queryByRole('menuitem', { name: 'Move start date…' })).toBeNull()
    expect(screen.queryByRole('menuitem', { name: /Complete cycle/ })).toBeNull()
    expect(screen.queryByRole('menuitem', { name: /Start cycle today/ })).toBeNull()
  })

  it('does not start the next cycle today while another cycle is current', async () => {
    const user = userEvent.setup()
    renderActions(c2, [c1, c2, c3])
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await screen.findByRole('menuitem', { name: 'Change cycle dates' })
    expect(screen.queryByRole('menuitem', { name: /Start cycle today/ })).toBeNull()
  })

  it('starts the next upcoming cycle today after a confirmation when no cycle is current', async () => {
    const user = userEvent.setup()
    const onStart = vi.fn().mockResolvedValue(undefined)
    const completed = { ...c1, status: 'completed' } as Cycle
    renderActions(c2, [completed, c2, c3], { onStart })
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Start cycle today…' }))
    const dialog = await screen.findByRole('dialog', { name: 'Start Cycle 2 today?' })
    expect(dialog).toHaveTextContent('The upcoming cycle’s start date will be moved to today.')
    await user.click(within(dialog).getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(onStart).toHaveBeenCalledTimes(1))
  })

  it("confirms an end moved into the next cycle, which moves that cycle's start", async () => {
    const user = userEvent.setup()
    const onUpdate = vi.fn().mockResolvedValue(undefined)
    renderActions(c1, [c1, c2, c3], { onUpdate })
    await openDates(user)
    await choose(user, 'Move end date…')
    const dialog = await screen.findByRole('dialog', { name: 'Change cycle end date' })
    await user.click(within(dialog).getByRole('button', { name: 'End date' }))
    await user.click(await screen.findByRole('button', { name: 'Next month' }))
    expect(dayButton(2026, 12, 13)).toBeDisabled()
    await user.click(dayButton(2026, 12, 4))
    await user.click(within(dialog).getByRole('button', { name: 'Apply' }))
    const confirm = await screen.findByRole('dialog', { name: 'Change end date for Cycle 1' })
    expect(confirm).toHaveTextContent('The next cycle’s start date will be moved to accommodate this change.')
    expect(onUpdate).not.toHaveBeenCalled()
    await user.click(within(confirm).getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(onUpdate).toHaveBeenCalledWith({ endsAt: '2026-12-04' }))
  })

  it('saves an earlier end that leaves the next cycle alone without asking', async () => {
    const user = userEvent.setup()
    const onUpdate = vi.fn().mockResolvedValue(undefined)
    renderActions(c1, [c1, c2, c3], { onUpdate })
    await openDates(user)
    await choose(user, 'Move end date…')
    const dialog = await screen.findByRole('dialog', { name: 'Change cycle end date' })
    await user.click(within(dialog).getByRole('button', { name: 'End date' }))
    await user.click(await screen.findByRole('button', { name: 'Previous month' }))
    expect(dayButton(2026, 10, 28)).toBeDisabled()
    await user.click(dayButton(2026, 10, 29))
    await user.click(within(dialog).getByRole('button', { name: 'Apply' }))
    await waitFor(() => expect(onUpdate).toHaveBeenCalledWith({ endsAt: '2026-10-29' }))
    expect(screen.queryByRole('dialog', { name: /Change end date for/ })).toBeNull()
  })

  it('asks how a moved start affects future cycles and names the previous cycle it shortens', async () => {
    const user = userEvent.setup()
    const onUpdate = vi.fn().mockResolvedValue(undefined)
    renderActions(c2, [c1, c2, c3], { onUpdate })
    await openDates(user)
    await choose(user, 'Move start date…')
    const dialog = await screen.findByRole('dialog', { name: 'Change cycle start date' })
    await user.click(within(dialog).getByRole('button', { name: 'Start date' }))
    await user.click(await screen.findByRole('button', { name: new Date(2026, 10, 9).toLocaleDateString() }))
    await user.click(within(dialog).getByRole('button', { name: 'Apply' }))
    const confirm = await screen.findByRole('dialog', { name: 'Change start date for Cycle 2' })
    expect(confirm).toHaveTextContent('The end date of Cycle 1 will be moved to accommodate this cycle’s new start date.')
    expect(within(confirm).getByRole('radio', { name: 'Change this cycle’s start date only' })).toBeChecked()
    await user.click(within(confirm).getByRole('radio', { name: 'Change this and future cycle start dates' }))
    await user.click(within(confirm).getByRole('button', { name: 'Confirm' }))
    await waitFor(() => expect(onUpdate).toHaveBeenCalledWith({ startsAt: '2026-11-09', shiftFollowing: true }))
  })
})
