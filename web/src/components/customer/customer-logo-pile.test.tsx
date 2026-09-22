import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { CustomerLogoPile } from './customer-logo-pile'

describe('CustomerLogoPile', () => {
  it('stacks up to maxVisible and shows overflow', () => {
    const customers = [
      { id: '1', name: 'Acme' },
      { id: '2', name: 'Beta' },
      { id: '3', name: 'Gamma' },
      { id: '4', name: 'Delta' },
    ]
    const { container } = render(<CustomerLogoPile customers={customers} maxVisible={3} />)
    expect(screen.getByLabelText('4 customers')).toBeTruthy()
    expect(container.querySelectorAll('.customer-logo-pile__mark')).toHaveLength(4) // 3 + overflow
    expect(screen.getByTitle('+1 more')).toBeTruthy()
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
})
