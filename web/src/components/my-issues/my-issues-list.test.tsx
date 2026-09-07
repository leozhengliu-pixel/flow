import { render, waitFor } from '@testing-library/react'
import { VirtuosoMockContext } from 'react-virtuoso'
import { describe, expect, it } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { MyIssuesList, type MyIssuesGroupData } from './my-issues-list'

describe('MyIssuesList virtualization', () => {
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
})
