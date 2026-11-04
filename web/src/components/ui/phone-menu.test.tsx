import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { PhoneMenu } from './phone-menu'

describe('PhoneMenu', () => {
  it('uses the shared phone popover edge without adding a visible heading', () => {
    render(<PhoneMenu open onOpenChange={vi.fn()} label="Display options"><button type="button">List</button></PhoneMenu>)
    expect(screen.getByRole('dialog', { name: 'Display options' })).toHaveAttribute('data-phone-popover')
    expect(document.querySelector('.phone-menu-heading')).toBeNull()
    expect(screen.getByRole('button', { name: 'List' })).toBeVisible()
  })

  it('shows a record node in the heading chip and a plain label for the default heading', () => {
    const { rerender } = render(<PhoneMenu open onOpenChange={vi.fn()} label="Change status" heading={<><span className="phone-menu-context-key">PIC-1 ⋅</span> Estimate test A</>}><button type="button">List</button></PhoneMenu>)
    const chip = document.querySelector('.phone-menu-heading > .phone-menu-context')!
    expect(chip).toHaveTextContent('PIC-1 ⋅ Estimate test A')
    expect(chip).toHaveAttribute('data-i18n-ignore')
    rerender(<PhoneMenu open onOpenChange={vi.fn()} label="Change status" heading><button type="button">List</button></PhoneMenu>)
    expect(document.querySelector('.phone-menu-heading > span')).toHaveTextContent('Change status')
    expect(document.querySelector('.phone-menu-heading > .phone-menu-context')).toBeNull()
  })
})
