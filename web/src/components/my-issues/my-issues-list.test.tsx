import { render, waitFor } from '@testing-library/react'
import { VirtuosoMockContext } from 'react-virtuoso'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { MyIssuesList, type MyIssuesGroupData } from './my-issues-list'

describe('MyIssuesList virtualization', () => {
  it('keeps repeated issues in separate groups independently keyed', async () => {
    const row: MyIssuesGroupData['issues'][number] = { id: 'shared', identifier: 'FLOW-1', title: 'Shared issue', href: '#shared', priority: 0, state: { id: 'started', name: 'In progress', type: 'started', color: '#f2c94c' }, createdAt: '', updatedAt: '' }
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      const { container } = render(<I18nProvider><VirtuosoMockContext.Provider value={{ viewportHeight: 440, itemHeight: 44 }}><MyIssuesList groups={Array.from({ length: 50 }, (_, index) => ({ id: `group-${index}`, label: `Group ${index}`, issues: [row] }))} displayProperties={new Set(['id'])}/></VirtuosoMockContext.Provider></I18nProvider>)
      await waitFor(() => expect(container.querySelectorAll('a').length).toBeGreaterThan(1))
      expect(errors.mock.calls.flat().join(' ')).not.toContain('same key')
    } finally { errors.mockRestore() }
  })
  it('nests sub-issues under their parent when nested sub-issues are on', async () => {
    const state = { id: 'started', name: 'In progress', type: 'started' as const, color: '#f2c94c' }
    const row = (id: string, parentId?: string): MyIssuesGroupData['issues'][number] => ({ id, identifier: `FLOW-${id}`, title: id, href: `#${id}`, priority: 0, state, parentId, createdAt: '2026-09-07T00:00:00.000Z', updatedAt: '2026-09-07T00:00:00.000Z' })
    const groups: MyIssuesGroupData[] = [{ id: 'started', label: 'In progress', issues: [row('parent'), row('child-1', 'parent'), row('child-2', 'parent'), row('other')] }]
    const view = (nestedSubIssues: boolean) => <I18nProvider><VirtuosoMockContext.Provider value={{ viewportHeight: 440, itemHeight: 44 }}><MyIssuesList groups={groups} nestedSubIssues={nestedSubIssues}/></VirtuosoMockContext.Provider></I18nProvider>
    const { container, rerender } = render(view(true))
    await waitFor(() => expect(container.querySelectorAll('a[data-issue-row-id]').length).toBe(4))
    const nested = () => [...container.querySelectorAll('a[data-issue-row-id]')].map(link => `${link.getAttribute('data-issue-row-id')}:${link.getAttribute('data-nested')}`)
    expect(nested()).toEqual(['parent:false', 'child-1:true', 'child-2:true', 'other:false'])
    rerender(view(false))
    await waitFor(() => expect(nested()).toEqual(['parent:false', 'child-1:false', 'child-2:false', 'other:false']))
  })

  it('keeps large lists bounded to the visible viewport', async () => {
    const groups: MyIssuesGroupData[] = [{
      id: 'started',
      label: 'In progress',
      stateType: 'started',
      issues: Array.from({ length: 120 }, (_, index) => ({
        id: `issue-${index}`,
        identifier: `FLOW-${index + 1}`,
        title: `Issue ${index + 1}`,
        href: `#issue-${index}`,
        priority: 0,
        state: { id: 'started', name: 'In progress', type: 'started', color: '#f2c94c' },
        createdAt: '2026-09-07T00:00:00.000Z',
        updatedAt: '2026-09-07T00:00:00.000Z',
      })),
    }]

    const { container } = render(
      <I18nProvider>
        <VirtuosoMockContext.Provider value={{ viewportHeight: 440, itemHeight: 44 }}>
          <MyIssuesList groups={groups}/>
        </VirtuosoMockContext.Provider>
      </I18nProvider>,
    )

    await waitFor(() => expect(container.querySelectorAll('a').length).toBeGreaterThan(0))
    expect(container.querySelectorAll('a').length).toBeLessThan(120)
    expect(container.querySelector('[data-virtuoso-scroller="true"]')).not.toBeNull()
  })

  it('renders milestone, customers, and customer revenue when those display properties are on', () => {
    const groups: MyIssuesGroupData[] = [{
      id: 'started',
      label: 'In progress',
      issues: [{
        id: 'issue-1',
        identifier: 'FLOW-1',
        title: 'Seed issue',
        href: '#issue-1',
        priority: 0,
        state: { id: 'started', name: 'In progress', type: 'started', color: '#f2c94c' },
        createdAt: '2026-09-07T00:00:00.000Z',
        updatedAt: '2026-09-07T00:00:00.000Z',
        projectMilestoneNames: ['车商城316迭代'],
        milestoneName: '车商城316迭代',
        customerNames: ['Acme'],
        customerRevenues: [12000],
        customers: [{ id: 'c1', name: 'Acme', annualRevenue: 12000 }],
        customerCount: 1,
        importantCustomerIds: ['c1'],
        customerRevenueSettings: { customerRevenueFormat: 'monthly', customerRevenueCurrency: 'EUR' },
      }],
    }]
    const { getByText, getByLabelText } = render(
      <I18nProvider>
        <MyIssuesList groups={groups} displayProperties={new Set(['id', 'milestone', 'customers', 'customerRevenue'])} />
      </I18nProvider>,
    )
    expect(getByLabelText('Milestone 车商城316迭代')).toBeVisible()
    // The reference app's Customers chip: ▲ important count, the logo pile and the single customer's name.
    expect(getByText('Acme')).toBeVisible()
    expect(getByLabelText('1 customer').textContent).toContain('1')
    // Customer revenue follows the workspace format (monthly → /12) and currency.
    expect(getByLabelText(/Customer revenue/).getAttribute('aria-label')).toMatch(/€1(\.0)?K\/mo/)
  })
})
