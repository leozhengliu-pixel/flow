import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { InboxListBoundary, InboxListEmpty, InboxListFilteredEmpty } from './inbox-list-state'

describe('inbox list states', () => {
  it('renders loading, error, empty, and content states', async () => {
    const retry = vi.fn()
    const { rerender } = render(<InboxListBoundary loading retry={retry}>Content</InboxListBoundary>)
    expect(screen.getByLabelText('Loading notifications')).toBeVisible()
    rerender(<InboxListBoundary error loading={false} retry={retry}>Content</InboxListBoundary>)
    await userEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(retry).toHaveBeenCalledOnce()
    rerender(<InboxListBoundary empty loading={false} retry={retry}>Content</InboxListBoundary>)
    expect(screen.getByText('All caught up')).toBeVisible()
    rerender(<InboxListBoundary loading={false} retry={retry}>Content</InboxListBoundary>)
    expect(screen.getByText('Content')).toBeVisible()
  })

  it('does not render an illustration for the root empty state', () => {
    const { container } = render(<InboxListEmpty onShowAll={vi.fn()} />)

    expect(screen.getByText('All caught up')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Show all notifications' })).toBeVisible()
    expect(container.querySelector('svg')).not.toBeInTheDocument()
  })

  it('keeps the distinct filtered-empty illustration', () => {
    const { container } = render(<I18nProvider><InboxListFilteredEmpty hiddenCount={3} onClear={vi.fn()} /></I18nProvider>)

    expect(container.querySelector('.flow-inbox-filter-empty-illustration')).toBeInTheDocument()
  })
})
