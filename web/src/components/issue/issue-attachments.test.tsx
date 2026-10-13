import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import type { Attachment, User } from '@/types/flow'

import { IssueAttachments } from './issue-attachments'

const creator = { id: 'user-1', name: 'Ada', displayName: 'Ada', email: 'ada@example.com', active: true } as User

const link = (overrides: Partial<Attachment> = {}): Attachment => ({
  id: 'attachment-1',
  issueId: 'issue-1',
  title: 'Ticket 42',
  url: 'https://support.example.com/tickets/42',
  contentType: 'text/uri-list',
  size: 0,
  createdAt: '2026-10-01T00:00:00Z',
  creator,
  ...overrides,
})

describe('IssueAttachments link attachments', () => {
  it('shows the subtitle and icon of API link attachments', () => {
    const { container } = render(<IssueAttachments attachments={[link({ subtitle: 'Solved', iconUrl: 'https://support.example.com/icon.png' })]} onRetry={vi.fn()} onDelete={vi.fn()} />)
    expect(screen.getByText('Ticket 42')).toBeTruthy()
    expect(screen.getByText('Solved')).toBeTruthy()
    expect(container.querySelector('img.attachment-link-icon')?.getAttribute('src')).toBe('https://support.example.com/icon.png')
  })

  it('falls back to the link host without a subtitle', () => {
    const { container } = render(<IssueAttachments attachments={[link()]} onRetry={vi.fn()} onDelete={vi.fn()} />)
    expect(screen.getByText('support.example.com')).toBeTruthy()
    expect(container.querySelector('img.attachment-link-icon')).toBeNull()
  })
})
