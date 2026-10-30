import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import type { TeamSettings } from '@/types/flow'
import { EstimatePicker } from './label-project-pickers'

const fibonacci = { teamId: 'team-1', estimateType: 'fibonacci' } as TeamSettings

describe('estimate picker', () => {
  it('opens the 228px estimate menu with "Set estimate to…", the ⇧ E keycaps and the scale rows', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<I18nProvider><EstimatePicker settings={fibonacci} onChange={onChange}/></I18nProvider>)
    const trigger = screen.getByRole('combobox', { name: 'Set estimate' })
    expect(trigger).toHaveTextContent('Set estimate')
    const triggerPath = trigger.querySelector('path')?.getAttribute('d')
    await user.click(trigger)
    const menu = await screen.findByRole('dialog', { name: 'Change Estimate' })
    expect(menu).toHaveClass('property-command-surface', 'property-command-estimate')
    expect(within(menu).getByPlaceholderText('Set estimate to…')).toBeInTheDocument()
    expect([...menu.querySelectorAll('.property-command-search kbd')].map(key => key.textContent)).toEqual(['⇧', 'E'])
    const rows = within(menu).getAllByRole('option')
    expect(rows.map(row => row.textContent)).toEqual(['No estimate', '1 Point', '2 Points', '3 Points', '5 Points', '8 Points'])
    const paths = rows.map(row => row.querySelector('.estimate-icon path')?.getAttribute('d'))
    expect(paths[0]).not.toBe(triggerPath)
    expect(paths.slice(1)).toEqual(Array(5).fill(triggerPath))
    expect(menu.querySelectorAll('[role=option] kbd')).toHaveLength(0)
    expect(rows[0]).toHaveAttribute('aria-checked', 'true')
    await user.click(within(menu).getByRole('option', { name: '5 Points' }))
    expect(onChange).toHaveBeenCalledWith(5)
  })

  it('checks the current estimate and offers to change it', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><EstimatePicker value={3} settings={{ ...fibonacci, estimateAllowZero: true, estimateExtended: true }} onChange={vi.fn()}/></I18nProvider>)
    const trigger = screen.getByRole('combobox', { name: 'Change estimate. Current estimate is 3 Points' })
    expect(trigger).toHaveTextContent('3 Points')
    await user.click(trigger)
    const menu = await screen.findByRole('dialog', { name: 'Change Estimate' })
    expect(within(menu).getByPlaceholderText('Change estimate to…')).toBeInTheDocument()
    expect(within(menu).getAllByRole('option').map(row => row.textContent)).toEqual(['No estimate', '0 Points', '1 Point', '2 Points', '3 Points', '5 Points', '8 Points', '13 Points', '21 Points'])
    expect(within(menu).getByRole('option', { name: '3 Points' })).toHaveAttribute('aria-checked', 'true')
    expect(menu.querySelector('[aria-checked=true] .property-command-check')).not.toBeNull()
  })
})
