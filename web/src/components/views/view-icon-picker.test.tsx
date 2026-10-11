import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'

import { ViewIconPicker } from './view-icon-picker'

describe('ViewIconPicker', () => {
  it('opens the reference app compact palette and changes the selected icon', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<I18nProvider><ViewIconPicker ariaLabel="Document icon" color="#8b8b90" icon="Page" onChange={onChange}/></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Document icon' }))
    expect(screen.getByRole('tab', { name: 'Icons' })).toHaveAttribute('aria-selected', 'true')
    // #8b8b90 is not one of the nine swatches, so the colour row opens on the custom colour.
    expect(screen.getByRole('button', { name: 'Selected color #8b8b90' })).toBeVisible()
    expect(screen.getByRole('textbox', { name: 'HEX color' })).toHaveValue('#8b8b90')
    expect(screen.getByRole('textbox', { name: 'Search icons…' })).toBeVisible()

    await user.click(screen.getByRole('button', { name: 'Automation' }))
    expect(onChange).toHaveBeenCalledWith({ icon: 'Automation', color: '#8b8b90' })
  })

  it('expands the custom color editor from the rainbow swatch', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><ViewIconPicker ariaLabel="Document icon" color="#95a2b3" icon="Page" onChange={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Document icon' }))
    expect(screen.getByRole('radio', { name: 'Dark Grey' })).toHaveAttribute('aria-checked', 'true')
    await user.click(screen.getByRole('button', { name: 'Set custom color' }))
    expect(screen.getByRole('textbox', { name: 'HEX color' })).toBeVisible()
  })

  it('keeps the swatch row fixed, moves the ✓ and saves the colour at once', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    const { rerender } = render(<I18nProvider><ViewIconPicker ariaLabel="Project icon" color="#eb5757" icon="Project" onChange={onChange}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Project icon' }))
    const names = () => screen.getAllByRole('radio').map(swatch => swatch.getAttribute('aria-label'))
    expect(names()).toEqual(['Grey', 'Dark Grey', 'Purple', 'Teal', 'Green', 'Yellow', 'Orange', 'Pink', 'Red'])
    await user.click(screen.getByRole('radio', { name: 'Yellow' }))
    expect(onChange).toHaveBeenCalledWith({ icon: 'Project', color: '#f2c94c' })
    rerender(<I18nProvider><ViewIconPicker ariaLabel="Project icon" color="#f2c94c" icon="Project" onChange={onChange}/></I18nProvider>)
    expect(names()).toEqual(['Grey', 'Dark Grey', 'Purple', 'Teal', 'Green', 'Yellow', 'Orange', 'Pink', 'Red'])
    expect(screen.getByRole('radio', { name: 'Yellow' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('radio', { name: 'Red' })).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByRole('textbox', { name: 'Search icons…' })).toBeVisible()
  })
})
