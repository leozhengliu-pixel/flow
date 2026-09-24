import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { makeIssue } from '@/test/fixtures'

const api = vi.hoisted(() => ({ fetchSimilarIssues: vi.fn(), listIssueRecords: vi.fn() }))
vi.mock('@/lib/api', () => api)

import { TooltipProvider } from '@/components/ui/tooltip'
import { SimilarIssues } from './similar-issues'

describe('SimilarIssues panel', () => {
  beforeEach(() => { api.fetchSimilarIssues.mockReset(); api.listIssueRecords.mockReset() })

  it('shows the server ranking, including description-only matches', async () => {
    const issue = makeIssue({ id: 'a', title: 'Checkout unresponsive' })
    const match = makeIssue({ id: 'b', identifier: 'FLOW-9', title: 'Tapping pay does nothing' })
    api.fetchSimilarIssues.mockResolvedValue({ results: [{ issue: match, score: 0.72, possibleDuplicate: true, matchedTerms: ['checkout'] }] })
    render(<TooltipProvider><SimilarIssues issue={issue} issues={[issue]} onOpen={vi.fn()} onMarkDuplicate={vi.fn()}/></TooltipProvider>)
    expect(await screen.findByText('Tapping pay does nothing')).toBeInTheDocument()
    expect(screen.getByText('Possible duplicates')).toBeInTheDocument()
    expect(api.listIssueRecords).not.toHaveBeenCalled()
  })

  it('falls back to local title similarity when the server is unavailable', async () => {
    const issue = makeIssue({ id: 'a', title: 'Export CSV totals wrong' })
    const local = makeIssue({ id: 'c', identifier: 'FLOW-3', title: 'Export CSV totals are wrong' })
    api.fetchSimilarIssues.mockRejectedValue(new Error('offline'))
    api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
    render(<TooltipProvider><SimilarIssues issue={issue} issues={[issue, local]} onOpen={vi.fn()} onMarkDuplicate={vi.fn()}/></TooltipProvider>)
    expect(await screen.findByText('Export CSV totals are wrong')).toBeInTheDocument()
  })
})
