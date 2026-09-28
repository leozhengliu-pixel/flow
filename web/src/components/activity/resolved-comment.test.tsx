import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { viewer } from '@/test/fixtures'
import type { Comment } from '@/types/flow'
import { ResolvedComment } from './resolved-comment'

const base = {
  id: 'c1',
  version: 1,
  body: 'Hello',
  reactions: {},
  createdAt: new Date().toISOString(),
  user: viewer,
} as Comment

describe('ResolvedComment', () => {
  it('resolves and shows thread summary when enabled', async () => {
    const onResolve = vi.fn()
    const comment = { ...base, resolved: true, threadSummary: { content: 'Decision: ship it' } }
    render(
      <I18nProvider>
        <ResolvedComment comment={comment} threadSummariesEnabled onResolve={onResolve}>
          <div>thread body</div>
        </ResolvedComment>
      </I18nProvider>,
    )
    expect(screen.getByText('Thread summary')).toBeTruthy()
    expect(screen.getByText('Decision: ship it')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: /Re-open/i }))
    expect(onResolve).toHaveBeenCalledWith(false)
  })

  it('adds no thread bar to open threads (they resolve from the comment menu)', () => {
    render(
      <I18nProvider>
        <ResolvedComment comment={base} onResolve={vi.fn()}>
          <div>open</div>
        </ResolvedComment>
      </I18nProvider>,
    )
    expect(screen.getByText('open')).toBeInTheDocument()
    expect(screen.queryByRole('group', { name: 'Comment thread' })).toBeNull()
  })
})
