import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { DateTimeControl, TimeControl } from './date-time-control'

describe('DateTimeControl', () => {
  it('selects dates without using a native date input', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<DateTimeControl label="Start date" value="2026-09-01" onChange={onChange}/>)
    await user.click(screen.getByRole('button', { name: 'Start date' }))
    expect(screen.getByRole('button', { name: '9/1/2026' })).toBeVisible()
    expect(document.querySelector('input[type="date"]')).toBeNull()
  })

  it('uses a portal select for time choices', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<TimeControl label="Time" value="10:00" onChange={onChange}/>)
    await user.click(screen.getByRole('combobox', { name: 'Time' }))
    await user.click(screen.getByRole('option', { name: '10:30' }))
    expect(onChange).toHaveBeenCalledWith('10:30')
  })
})
