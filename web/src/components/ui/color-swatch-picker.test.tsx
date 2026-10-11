import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { FLOW_COLOR_PALETTE } from './color-palette'
import { ColorSwatchPicker } from './color-swatch-picker'

const NAMES = ['Grey', 'Dark Grey', 'Purple', 'Teal', 'Green', 'Yellow', 'Orange', 'Pink', 'Red']

function Harness({ initial, onChange = vi.fn(), note }: { initial: string; onChange?: (color: string, source: string) => void; note?: string }) {
  const [color, setColor] = useState(initial)
  return <ColorSwatchPicker color={color} note={note} onChange={(next, source) => { setColor(next); onChange(next, source) }}/>
}

const swatchNames = () => screen.getAllByRole('radio').map(swatch => swatch.getAttribute('aria-label'))
const checked = () => screen.getAllByRole('radio').filter(swatch => swatch.getAttribute('aria-checked') === 'true').map(swatch => swatch.getAttribute('aria-label'))

describe('ColorSwatchPicker', () => {
  it('shows the reference app\'s fixed nine-colour palette with the ✓ on the current colour', () => {
    render(<I18nProvider><Harness initial="#F2C94C"/></I18nProvider>)
    expect(swatchNames()).toEqual(NAMES)
    expect(checked()).toEqual(['Yellow'])
    expect(screen.getByRole('radio', { name: 'Yellow' }).querySelector('svg')).not.toBeNull()
    expect(screen.getByRole('radio', { name: 'Grey' }).querySelector('svg')).toBeNull()
    expect(screen.queryByRole('button', { name: /Selected color/ })).not.toBeInTheDocument()
  })

  it('moves the ✓ to the picked swatch and keeps the palette fixed', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<I18nProvider><Harness initial="#eb5757" onChange={onChange}/></I18nProvider>)
    await user.click(screen.getByRole('radio', { name: 'Teal' }))
    expect(onChange).toHaveBeenCalledWith('#26b5ce', 'swatch')
    expect(swatchNames()).toEqual(NAMES)
    expect(checked()).toEqual(['Teal'])
    expect(screen.getByRole('radio', { name: 'Grey' })).toHaveAttribute('data-color', FLOW_COLOR_PALETTE[0].value)
  })

  it('opens on the custom colour panel when the colour is not a swatch', () => {
    render(<I18nProvider><Harness initial="#bb87fc"/></I18nProvider>)
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Selected color #bb87fc' })).toBeVisible()
    expect(screen.getByRole('textbox', { name: 'HEX color' })).toHaveValue('#bb87fc')
    expect(screen.getByRole('button', { name: 'Select default color' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('slider', { name: 'Saturation and brightness' })).toBeInTheDocument()
    expect(screen.getByRole('slider', { name: 'Hue' })).toBeInTheDocument()
  })

  it('switches between the swatches and the custom panel without changing the colour', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<I18nProvider><Harness initial="#bb87fc" onChange={onChange}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Select default color' }))
    expect(swatchNames()).toEqual(NAMES)
    expect(checked()).toEqual([])
    expect(screen.getByRole('button', { name: 'Set custom color' })).toHaveAttribute('aria-pressed', 'false')
    await user.click(screen.getByRole('button', { name: 'Set custom color' }))
    expect(screen.getByRole('textbox', { name: 'HEX color' })).toHaveValue('#bb87fc')
    expect(onChange).not.toHaveBeenCalled()
  })

  it('commits a typed HEX colour on Enter, and a 3-digit colour expands', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<I18nProvider><Harness initial="#95a2b3" onChange={onChange}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Set custom color' }))
    const hex = screen.getByRole('textbox', { name: 'HEX color' })
    await user.clear(hex)
    await user.type(hex, '#123abc{Enter}')
    expect(onChange).toHaveBeenLastCalledWith('#123abc', 'custom')
    await user.clear(hex)
    await user.type(hex, 'f0a{Enter}')
    expect(onChange).toHaveBeenLastCalledWith('#ff00aa', 'custom')
    expect(screen.getByRole('button', { name: 'Selected color #ff00aa' })).toBeVisible()
  })

  it('restores the committed colour when the HEX field is left invalid', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<I18nProvider><Harness initial="#bb87fc" onChange={onChange}/></I18nProvider>)
    const hex = screen.getByRole('textbox', { name: 'HEX color' })
    await user.clear(hex)
    await user.type(hex, 'nope')
    await user.tab()
    expect(hex).toHaveValue('#bb87fc')
    expect(onChange).not.toHaveBeenCalled()
  })

  it('shows a note after the HEX field', () => {
    render(<I18nProvider><Harness initial="#e2e2e2" note="Contrast has been adjusted"/></I18nProvider>)
    expect(screen.getByText('Contrast has been adjusted')).toBeVisible()
  })

  it('lets the keyboard reach every swatch and the custom toggle with Tab', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><Harness initial="#eb5757"/></I18nProvider>)
    await user.tab()
    expect(screen.getByRole('radio', { name: 'Grey' })).toHaveFocus()
    for (let index = 1; index < NAMES.length; index++) await user.tab()
    expect(screen.getByRole('radio', { name: 'Red' })).toHaveFocus()
    await user.tab()
    expect(screen.getByRole('button', { name: 'Set custom color' })).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(screen.getByRole('textbox', { name: 'HEX color' })).toBeVisible()
  })
})
