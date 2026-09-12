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
        customerNames: ['Acme'],
        customerRevenues: [12000],
      }],
    }]
    const { getByText, getByLabelText } = render(
      <I18nProvider>
        <MyIssuesList groups={groups} displayProperties={new Set(['id', 'milestone', 'customers', 'customerRevenue'])} />
      </I18nProvider>,
    )
    expect(getByLabelText('Milestone 车商城316迭代')).toBeVisible()
    expect(getByText('Acme')).toBeVisible()
    expect(getByLabelText(/Customer revenue/)).toBeVisible()
  })
})
