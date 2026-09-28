import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { customersForInitiative, DetailLabelControl } from './detail-label-control'

describe('DetailLabelControl', () => {
  it('exposes changeLabelAction and host', () => {
    const { container } = render(
      <DetailLabelControl changeLabelAction="changeLabelAction" host="issue">
        <button type="button">Add label</button>
      </DetailLabelControl>,
    )
    const root = container.querySelector('.detail-label-control')
    expect(root?.getAttribute('data-change-label-action')).toBe('changeLabelAction')
    expect(root?.getAttribute('data-host')).toBe('issue')
  })

  it('gates interaction when archived', () => {
    const onClick = vi.fn()
    render(
      <DetailLabelControl host="project" isArchived>
        <button type="button" onClick={onClick}>Add label</button>
      </DetailLabelControl>,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Add label' }))
    expect(onClick).not.toHaveBeenCalled()
  })

  it('renders CustomerLogoPile on initiative host', () => {
    render(
      <DetailLabelControl
        host="initiative"
        customers={[{ id: 'c1', name: 'Acme' }, { id: 'c2', name: 'Beta' }]}
      >
        <span>picker</span>
      </DetailLabelControl>,
    )
    expect(screen.getByLabelText('2 customers')).toBeTruthy()
  })
})

describe('customersForInitiative', () => {
  it('merges project customer names and request links', () => {
    const result = customersForInitiative(
      { projectIds: ['p1', 'p2'] },
      [
        { id: 'p1', customers: ['Acme'] },
        { id: 'p2', customers: ['Ghost'] },
      ],
      [
        { id: 'c1', name: 'Acme' },
        { id: 'c2', name: 'Beta' },
      ],
      [
        { projectId: 'p1', customerId: 'c2' },
        { projectId: 'p1', customerId: 'c1', archivedAt: '2026-01-01' },
        { projectId: 'other', customerId: 'c2' },
      ],
    )
    expect(result.map((item) => item.id)).toEqual(['c1', 'c2'])
  })
})
