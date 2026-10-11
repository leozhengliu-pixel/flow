import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { StatusColorPicker } from './issues-projects-settings'

afterEach(() => { delete document.documentElement.dataset.theme })

const open = async (user: ReturnType<typeof userEvent.setup>) => user.click(screen.getByRole('button', { name: 'Color' }))

describe('workflow / customer status colour picker', () => {
  it('uses the fixed swatches, saves the picked one and closes', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<I18nProvider><StatusColorPicker color="#f2c94c" type="started" onChange={onChange}/></I18nProvider>)
    await open(user)
    expect(screen.getAllByRole('radio').map(swatch => swatch.getAttribute('aria-label'))).toEqual(['Grey', 'Dark Grey', 'Purple', 'Teal', 'Green', 'Yellow', 'Orange', 'Pink', 'Red'])
    expect(screen.getByRole('radio', { name: 'Yellow' })).toHaveAttribute('aria-checked', 'true')
    await user.click(screen.getByRole('radio', { name: 'Green' }))
    expect(onChange).toHaveBeenCalledWith('#4cb782')
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
  })

  it('opens a custom status colour on the custom panel and notes the light-theme contrast adjustment', async () => {
    document.documentElement.dataset.theme = 'light'
    const user = userEvent.setup()
    render(<I18nProvider><StatusColorPicker adjustContrast color="#e2e2e2" type="unstarted" onChange={vi.fn()}/></I18nProvider>)
    await open(user)
    expect(screen.getByRole('textbox', { name: 'HEX color' })).toHaveValue('#e2e2e2')
    expect(screen.getByText('Contrast has been adjusted')).toBeVisible()
  })

  it('has no contrast note on dark surfaces or for customer statuses', async () => {
    const user = userEvent.setup()
    const { unmount } = render(<I18nProvider><StatusColorPicker adjustContrast color="#e2e2e2" type="unstarted" onChange={vi.fn()}/></I18nProvider>)
    await open(user)
    expect(screen.queryByText('Contrast has been adjusted')).not.toBeInTheDocument()
    unmount()
    document.documentElement.dataset.theme = 'light'
    render(<I18nProvider><StatusColorPicker color="#e2e2e2" type="status" onChange={vi.fn()}/></I18nProvider>)
    await open(user)
    expect(screen.queryByText('Contrast has been adjusted')).not.toBeInTheDocument()
  })

  it('commits a typed HEX colour and keeps the picker open', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<I18nProvider><StatusColorPicker color="#bec2c8" type="backlog" onChange={onChange}/></I18nProvider>)
    await open(user)
    await user.click(screen.getByRole('button', { name: 'Set custom color' }))
    const hex = screen.getByRole('textbox', { name: 'HEX color' })
    await user.clear(hex)
    await user.type(hex, '#336699{Enter}')
    expect(onChange).toHaveBeenCalledWith('#336699')
    expect(hex).toBeVisible()
  })
})
