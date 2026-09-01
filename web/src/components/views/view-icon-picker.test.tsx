import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'

import { ViewIconPicker } from './view-icon-picker'

describe('ViewIconPicker', () => {
  it('opens the Linear compact palette and changes the selected icon', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<I18nProvider><ViewIconPicker ariaLabel="Document icon" color="#8b8b90" icon="Page" onChange={onChange}/></I18nProvider>)

    await user.click(screen.getByRole('button', { name: 'Document icon' }))
    expect(screen.getByRole('tab', { name: 'Icons' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('button', { name: /Selected color/ })).toBeVisible()
    expect(screen.getByRole('textbox', { name: 'Search icons…' })).toBeVisible()

    await user.click(screen.getByRole('button', { name: 'Automation' }))
    expect(onChange).toHaveBeenCalledWith({ icon: 'Automation', color: '#8b8b90' })
  })

  it('expands the custom color editor from the rainbow swatch', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><ViewIconPicker ariaLabel="Document icon" color="#8b8b90" icon="Page" onChange={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Document icon' }))
    await user.click(screen.getByRole('button', { name: 'Set custom color' }))
    expect(screen.getByRole('textbox', { name: 'HEX color' })).toBeVisible()
  })
})
