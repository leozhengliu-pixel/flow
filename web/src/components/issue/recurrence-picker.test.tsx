import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { RecurrencePicker } from './recurrence-picker'

const today = new Date(2026, 8, 29) // Tuesday

function setup(props: Partial<React.ComponentProps<typeof RecurrencePicker>> = {}) {
  const onSave = vi.fn()
  const onStop = vi.fn()
  const onCancel = vi.fn()
  render(<I18nProvider><RecurrencePicker today={today} onSave={onSave} onStop={onStop} onCancel={onCancel} {...props}/></I18nProvider>)
  return { onSave, onStop, onCancel }
}

async function choose(user: ReturnType<typeof userEvent.setup>, label: string, option: string) {
  await user.click(screen.getByRole('combobox', { name: label }))
  await user.click(await screen.findByRole('option', { name: option }))
}

describe('recurrence picker', () => {
  beforeEach(() => { localStorage.clear(); vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }) })
  afterEach(() => vi.unstubAllGlobals())

  it('leads with Linear\'s first due date (one week out) and repeats every 1 week', async () => {
    const user = userEvent.setup()
    const { onSave } = setup()
    expect(screen.getByLabelText('First due')).toHaveValue('2026-10-06')
    expect(screen.getByRole('combobox', { name: 'Repeat interval' })).toHaveTextContent('1')
    expect(screen.getByRole('combobox', { name: 'Repeat unit' })).toHaveTextContent('week')
    expect(screen.getByRole('button', { name: 'Tuesday' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByText('Repeats every week')).toBeInTheDocument()
    expect(screen.getByText('Next due Oct 13')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Stop recurring' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSave).toHaveBeenCalledWith('FREQ=WEEKLY', '2026-10-06')
  })

  it('sends "repeats every N unit" anchored on the chosen first due date', async () => {
    const user = userEvent.setup()
    const { onSave } = setup()
    fireEvent.change(screen.getByLabelText('First due'), { target: { value: '2026-10-09' } })
    await choose(user, 'Repeat interval', '2')
    await choose(user, 'Repeat unit', 'months')
    expect(screen.getByText('Repeats every 2 months')).toBeInTheDocument()
    expect(screen.getByText('Next due Dec 9')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSave).toHaveBeenCalledWith('FREQ=MONTHLY;INTERVAL=2', '2026-10-09')
  })

  it('keeps Flow\'s extra weekday options below the Linear row', async () => {
    const user = userEvent.setup()
    const { onSave } = setup()
    await user.click(screen.getByRole('button', { name: 'Monday' }))
    await user.click(screen.getByRole('button', { name: 'Friday' }))
    await user.click(screen.getByRole('button', { name: 'Tuesday' }))
    await choose(user, 'Repeat interval', '2')
    expect(screen.getByText('Every 2 weeks on Mon, Fri')).toBeInTheDocument()
    expect(screen.getByText('Next due Oct 9')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSave).toHaveBeenCalledWith('FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,FR', '2026-10-06')
  })

  it('offers the weekdays shortcut and blocks saving without a weekday', async () => {
    const user = userEvent.setup()
    const { onSave } = setup()
    await user.click(screen.getByRole('button', { name: 'Tuesday' }))
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Weekdays' }))
    expect(screen.getByText('Every weekday')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSave).toHaveBeenCalledWith('weekdays', '2026-10-06')
  })

  it('edits an existing schedule from the issue due date and can stop recurring', async () => {
    const user = userEvent.setup()
    const { onSave, onStop } = setup({ value: 'FREQ=MONTHLY;BYDAY=-1FR', dueDate: '2026-10-30' })
    expect(screen.getByLabelText('First due')).toHaveValue('2026-10-30')
    const summary = screen.getByText('Monthly on the last Friday').closest('p')!
    expect(within(summary).getByText('Next due Nov 27')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Stop recurring' }))
    expect(onStop).toHaveBeenCalledOnce()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSave).toHaveBeenCalledWith('FREQ=MONTHLY;BYDAY=-1FR', '2026-10-30')
  })

  it('reads legacy schedules without a due date from their next occurrence', () => {
    setup({ value: 'FREQ=MONTHLY;BYMONTHDAY=31', nextOccurrenceAt: '2027-01-31', today: new Date(2027, 0, 20) })
    expect(screen.getByLabelText('First due')).toHaveValue('2027-01-31')
    expect(screen.getByText('Repeats every month')).toBeInTheDocument()
    expect(screen.getByText('Next due Feb 28')).toBeInTheDocument()
  })
})
