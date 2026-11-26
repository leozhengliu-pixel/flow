import { fireEvent, render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import type { Attachment, User } from '@/types/flow'

import { IssueResources } from './issue-resources'

const creator = { id: 'user-1', name: 'Ada', displayName: 'Ada', email: 'ada@example.com', active: true } as User

const link = (url: string, overrides: Partial<Attachment> = {}): Attachment => ({
  id: 'attachment-1',
  issueId: 'issue-1',
  title: 'Reference',
  url,
  contentType: 'text/uri-list',
  size: 0,
  createdAt: '2026-10-01T00:00:00Z',
  creator,
  ...overrides,
})

const resources = (links: Attachment[]) => (
  <I18nProvider>
    <IssueResources
      documents={[]}
      issueId="issue-1"
      issueKey="FLOW-1"
      links={links}
      onDeleteLink={vi.fn()}
      workspaceKey="test-workspace"
    />
  </I18nProvider>
)

describe('IssueResources link favicons', () => {
  it('requests the authenticated favicon endpoint for an ordinary link', () => {
    const { container } = render(resources([link('https://docs.example.com/guide?tab=api')]))
    const image = container.querySelector('.res-icon img')
    expect(image?.getAttribute('src')).toBe('/api/link-icon?workspace=test-workspace&issueId=issue-1&attachmentId=attachment-1')
    expect(image?.getAttribute('loading')).toBe('lazy')
    expect(image?.getAttribute('referrerpolicy')).toBe('no-referrer')
  })

  it('keeps brand icons and existing attachment icons on their current paths', () => {
    const { container } = render(resources([
      link('https://github.com/flow/issues/1'),
      link('https://docs.example.com/guide', { id: 'attachment-icon', iconUrl: '/uploads/custom.png' }),
    ]))
    expect(container.querySelector('.res-icon[data-brand="github"] svg')).not.toBeNull()
    const images = [...container.querySelectorAll('.res-icon img')].map(image => image.getAttribute('src'))
    expect(images).toEqual(['/uploads/custom.png'])
  })

  it('shows the glyph after an error and retries when the attachment changes', () => {
    const { container, rerender } = render(resources([link('https://docs.example.com/guide')]))
    fireEvent.error(container.querySelector('.res-icon img')!)
    expect(container.querySelector('.res-icon .res-link-glyph')).not.toBeNull()
    expect(container.querySelector('.res-icon img')).toBeNull()

    rerender(resources([link('https://example.org/guide', { updatedAt: '2026-10-02T00:00:00Z' })]))
    expect(container.querySelector('.res-icon img')?.getAttribute('src')).toContain('&v=2026-10-02T00%3A00%3A00Z')
    expect(container.querySelector('.res-icon .res-link-glyph')).toBeNull()
  })
})
