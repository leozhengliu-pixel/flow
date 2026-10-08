import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { CustomerLogoPile } from './customer-logo-pile'
import { CustomerLogo } from './customer-logo'

describe('CustomerLogoPile', () => {
  it('stacks up to maxVisible logos, first on top, with no overflow counter (Linear)', () => {
    const customers = [
      { id: '1', name: 'Acme' },
      { id: '2', name: 'Beta' },
      { id: '3', name: 'Gamma' },
      { id: '4', name: 'Delta' },
    ]
    const { container } = render(<CustomerLogoPile customers={customers} maxVisible={3} size={18} />)
    expect(screen.getByLabelText('4 customers')).toBeTruthy()
    const items = [...container.querySelectorAll<HTMLElement>('.customer-logo-pile__item')]
    expect(items).toHaveLength(3)
    expect(items.map(item => item.style.zIndex)).toEqual(['3', '2', '1'])
    expect(items[0].style.marginRight).toBe('-7.2px')
    expect(items[2].style.marginRight).toBe('0px')
    expect(container.textContent).toBe('ABG')
  })

  it('returns null when empty and no append slot', () => {
    const { container } = render(<CustomerLogoPile customers={[]} />)
    expect(container.firstChild).toBeNull()
  })

  it('can append a no-customer slot', () => {
    render(<CustomerLogoPile appendNoCustomer customers={[]} />)
    expect(screen.getByLabelText('No customers')).toBeTruthy()
    expect(screen.getByTitle('No customer')).toBeTruthy()
  })

  it('reserves the width of maxVisible logos when stableWidth is set', () => {
    render(<CustomerLogoPile customers={[{ id: '1', name: 'Acme' }]} maxVisible={3} size={20} stableWidth />)
    expect(screen.getByLabelText('1 customers').style.minWidth).toBe('41px')
  })
})

describe('CustomerLogo', () => {
  it('draws the logo 2px smaller on its tile, or the first letter when there is no logo', () => {
    const { container, rerender } = render(<CustomerLogo customer={{ id: '1', name: 'acme', logoUrl: 'https://example.com/a.png' }} size={16} />)
    const image = container.querySelector('img')!
    expect(image.getAttribute('width')).toBe('14')
    expect(container.querySelector('.customer-tile__backdrop')).toBeTruthy()
    rerender(<CustomerLogo customer={{ id: '1', name: 'acme' }} size={16} />)
    expect(container.querySelector('.customer-tile__letter')?.textContent).toBe('A')
    rerender(<CustomerLogo customer={null} size={16} />)
    expect(container.querySelector('.customer-tile__default svg')).toBeTruthy()
  })
})
