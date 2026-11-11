import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { IssueSLA, IssueUpdateInput } from '@/types/flow'
import { IssueSLAPicker } from './issue-sla-picker'

const sla: IssueSLA = { id: 'sla', issueId: 'issue', ruleId: 'rule', status: 'active', startedAt: '2026-11-10T10:00:00Z', dueAt: '2026-11-12T10:00:00Z', pausedMinutes: 0, remainingMinutes: 60 }
beforeEach(() => { localStorage.setItem('flow:locale', 'en-US') })
afterEach(() => vi.restoreAllMocks())

describe('issue SLA picker', () => {
  it('sets a preset deadline through the issue API contract', async () => {
    const user = userEvent.setup(), onChange = vi.fn(async (_input: IssueUpdateInput) => undefined)
    render(<I18nProvider><IssueSLAPicker onChange={onChange}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Set SLA…' }))
    const before = Date.now()
    await user.click(screen.getByRole('option', { name: '24 hours' }))
    expect(onChange).toHaveBeenCalledWith({ slaBreachesAt: expect.any(String), slaType: 'all' })
    expect(Math.abs(Date.parse(onChange.mock.calls[0][0].slaBreachesAt!) - before - 86_400_000)).toBeLessThan(2000)
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('removes an existing SLA and keeps the dialog available after a failed write', async () => {
    const user = userEvent.setup(), onChange = vi.fn().mockRejectedValueOnce(new Error('Connection lost')).mockResolvedValue(undefined)
    render(<I18nProvider><IssueSLAPicker sla={sla} onChange={onChange}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Change SLA…' }))
    await user.click(screen.getByRole('option', { name: 'Remove SLA' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Connection lost')
    expect(screen.getByRole('dialog', { name: 'Set SLA' })).toBeVisible()
    await user.click(screen.getByRole('option', { name: 'Remove SLA' }))
    expect(onChange).toHaveBeenLastCalledWith({ slaBreachesAt: '', slaType: 'all' })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('accepts typed dates without submitting IME confirmation', async () => {
    const user = userEvent.setup(), onChange = vi.fn(async () => undefined)
    render(<I18nProvider><IssueSLAPicker onChange={onChange}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Set SLA…' }))
    await user.type(screen.getByRole('textbox'), 'in 3 hours')
    fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter', isComposing: true })
    expect(onChange).not.toHaveBeenCalled()
    await user.keyboard('{Enter}')
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ slaBreachesAt: expect.any(String) })))
  })

  it('disables the control on a closed or read-only issue', () => {
    render(<I18nProvider><IssueSLAPicker sla={sla} disabled onChange={vi.fn()}/></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Change SLA…' })).toBeDisabled()
  })
})
