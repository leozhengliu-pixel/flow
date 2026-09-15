import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { IssueLoadingPreview } from './issue-loading-preview'
import { makeIssue } from '@/test/fixtures'

describe('IssueLoadingPreview', () => {
  it('keeps authorized issue content read-only before the detail editor mounts', () => {
    const onBack = vi.fn()
    render(<IssueLoadingPreview issue={makeIssue({ identifier: 'HAI-75696', title: 'Preview only', description: 'Body arrives early', isSummary: false })} onBack={onBack} />)
    expect(screen.getByRole('region', { name: 'Issue preview' })).toHaveAttribute('aria-busy', 'true')
    expect(screen.getByRole('heading', { name: 'Preview only' })).toBeInTheDocument()
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(document.querySelector('[contenteditable="true"]')).toBeNull()
    expect(document.querySelector('.issue-loading-preview .issue-layout')).toBeInTheDocument()
    expect(document.querySelector('.issue-loading-preview .issue-document .issue-title-field .title-editor')).toBeInTheDocument()
    expect(document.querySelector('.issue-loading-preview .issue-description-root .description-editor')).toBeInTheDocument()
    expect(document.querySelector('.issue-loading-preview .issue-properties')).toBeInTheDocument()
    screen.getByRole('button', { name: 'Back' }).click()
    expect(onBack).toHaveBeenCalledTimes(1)
  })

  it('does not promote summary records into the persistable preview surface', () => {
    render(<IssueLoadingPreview issue={makeIssue({ isSummary: true, title: 'Summary only' })} onBack={vi.fn()} />)
    expect(screen.getByRole('status')).toHaveTextContent('Loading issue…')
    expect(screen.queryByRole('heading', { name: 'Summary only' })).not.toBeInTheDocument()
  })
})
