import { render } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue, viewer } from '@/test/fixtures'
import type { ActivityEvent } from '@/types/flow'
import { ActivityTimeline } from './activity-timeline'

vi.mock('@/hooks/use-phone-layout', () => ({ usePhoneLayout: () => true }))

describe('phone issue activity', () => {
  it('renders plain issue links and distinct relationship glyphs', () => {
    const target = makeIssue({ id: 'target', identifier: 'MOB-2', title: '[mobile] Related issue' })
    const context = makeBootstrap({ issues: [target] })
    const events: ActivityEvent[] = ['blocks', 'related'].map((type, index) => ({ id: `event-${index}`, type: 'issue.relation_added', actor: viewer, createdAt: new Date(2026, 9, 1, 0, index).toISOString(), metadata: { type, relatedIssueId: target.id } }))
    const { container } = render(<MemoryRouter><I18nProvider><ActivityTimeline events={events} comments={[]} context={context} viewerId={viewer.id} onReply={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onReaction={vi.fn()}/></I18nProvider></MemoryRouter>)
    const links = container.querySelectorAll('.activity-relation-link')
    expect(links).toHaveLength(2)
    for (const link of links) {
      expect(link).toHaveTextContent('MOB-2 [mobile] Related issue')
      expect(link.getAttribute('href')).toContain('/issue/MOB-2')
    }
    expect(container.querySelector('[data-action-glyph=blocking] path')).toHaveAttribute('d', expect.stringContaining('M10.7461 1'))
    expect(container.querySelector('[data-action-glyph=related] path')).toHaveAttribute('d', expect.stringContaining('m8.88 7'))
  })
})
