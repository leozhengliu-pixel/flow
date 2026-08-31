import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { InboxListBoundary } from './inbox-list-state'

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
})
