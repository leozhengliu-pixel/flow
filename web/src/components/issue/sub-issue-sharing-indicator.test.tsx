/** @vitest-environment jsdom */
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import {
  SubIssueSharingIndicator,
  resolveSubIssueSharingState,
} from './sub-issue-sharing-indicator'

describe('SubIssueSharingIndicator (LS-0566)', () => {
  it('resolves inherited share state', () => {
    const state = resolveSubIssueSharingState({
      issue: { id: 'child', parentId: 'parent' },
      parentIssue: { id: 'parent', shareToken: 'tok' },
      sharedUsers: [{ id: 'u1', displayName: 'Ada' }],
    })
    expect(state.visible).toBe(true)
    expect(state.tooltip).toMatch(/Shared with parent/)
  })

  it('renders indicator for shared sub-issue', () => {
    const onOpen = vi.fn()
    render(
      <SubIssueSharingIndicator
        issue={{ id: 'child', parentId: 'parent' }}
        parentIssue={{ id: 'parent', shareToken: 'tok' }}
        sharedUsers={[{ id: 'u1', displayName: 'Ada' }]}
        onOpenSharing={onOpen}
      />,
    )
    expect(screen.getByRole('button', { name: /Shared with parent/i })).toBeInTheDocument()
  })
})
