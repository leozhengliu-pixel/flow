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
})
