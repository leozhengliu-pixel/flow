import { render, screen, within } from '@testing-library/react'
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

describe('recurrence picker', () => {
  beforeEach(() => { localStorage.clear(); vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }) })
  afterEach(() => vi.unstubAllGlobals())

  it('defaults to weekly on today and starts tomorrow', async () => {
    const user = userEvent.setup()
    const { onSave } = setup()
    expect(screen.getByRole('button', { name: 'Tuesday' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByLabelText('Start date')).toHaveValue('2026-09-30')
    expect(screen.getByText('Weekly on Tue')).toBeInTheDocument()
    expect(screen.getByText(/Next: Oct 6/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Stop recurring' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSave).toHaveBeenCalledWith('FREQ=WEEKLY;BYDAY=TU', '2026-09-30')
  })

  it('builds every-N-weeks schedules on several weekdays', async () => {
    const user = userEvent.setup()
    const { onSave } = setup()
    await user.click(screen.getByRole('button', { name: 'Monday' }))
    await user.click(screen.getByRole('button', { name: 'Friday' }))
    await user.click(screen.getByRole('button', { name: 'Tuesday' }))
    const interval = screen.getByRole('spinbutton', { name: 'Interval' })
    await user.tripleClick(interval)
    await user.keyboard('2')
    expect(screen.getByText('Every 2 weeks on Mon, Fri')).toBeInTheDocument()
    expect(screen.getByText(/Next: Oct 2/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSave).toHaveBeenCalledWith('FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,FR', '2026-09-30')
  })

  it('offers the weekdays shortcut and blocks saving without a weekday', async () => {
    const user = userEvent.setup()
    const { onSave } = setup()
    await user.click(screen.getByRole('button', { name: 'Tuesday' }))
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Weekdays' }))
    expect(screen.getByText('Every weekday')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSave).toHaveBeenCalledWith('weekdays', '2026-09-30')
  })

  it('edits an existing monthly schedule and can stop recurring', async () => {
    const user = userEvent.setup()
    const { onSave, onStop } = setup({ value: 'FREQ=MONTHLY;BYDAY=-1FR', nextOccurrenceAt: '2026-10-30' })
    expect(screen.getByText('Monthly on the last Friday')).toBeInTheDocument()
    expect(screen.getByLabelText('Start date')).toHaveValue('2026-10-30')
    const summary = screen.getByText('Monthly on the last Friday').closest('p')!
    expect(within(summary).getByText(/Next: Oct 30/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Stop recurring' }))
    expect(onStop).toHaveBeenCalledOnce()
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSave).toHaveBeenCalledWith('FREQ=MONTHLY;BYDAY=-1FR', '2026-10-30')
  })

  it('clamps day 31 to the end of shorter months in the preview', () => {
    setup({ value: 'FREQ=MONTHLY;BYMONTHDAY=31', nextOccurrenceAt: '2027-02-01', today: new Date(2027, 0, 20) })
    expect(screen.getByText('Monthly on day 31')).toBeInTheDocument()
    expect(screen.getByText(/Next: Feb 28/)).toBeInTheDocument()
  })
})
