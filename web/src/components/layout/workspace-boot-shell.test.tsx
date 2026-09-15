import { render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { IssueLoadingPreview } from '@/components/issue/issue-loading-preview'
import { SidebarShell } from '@/components/layout/sidebar-shell'
import { WorkspaceBootShell } from '@/components/layout/workspace-boot-shell'
import { ErrorState } from '@/components/state/state-view'
import { makeIssue } from '@/test/fixtures'

describe('workspace boot shell', () => {
  beforeEach(() => {
    vi.stubGlobal('matchMedia', () => Object.assign(new EventTarget(), { matches: false, media: '(max-width: 1024px)' }))
  })
  afterEach(() => { vi.unstubAllGlobals() })

  it('never leaves a blank left nav while issue preview arrives before bootstrap', () => {
    const issue = makeIssue({ identifier: 'HAI-75696', title: 'Sidebar should stay visible', isSummary: false })
    const onBack = vi.fn()
    render(
      <WorkspaceBootShell>
        <IssueLoadingPreview issue={issue} onBack={onBack} />
      </WorkspaceBootShell>,
    )

    expect(screen.getByRole('navigation', { name: 'Loading workspace navigation' })).toBeInTheDocument()
    expect(screen.getByRole('navigation', { name: 'Loading workspace navigation' }).querySelectorAll('.sidebar-shell-link').length).toBeGreaterThan(0)
    expect(screen.getByRole('region', { name: 'Issue preview' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Sidebar should stay visible' })).toBeInTheDocument()
    expect(screen.getByText('HAI-75696')).toBeInTheDocument()
  })

  it('renders a visible sidebar shell skeleton on its own', () => {
    render(<SidebarShell label="Loading account navigation" />)
    const nav = screen.getByRole('navigation', { name: 'Loading account navigation' })
    expect(nav).toHaveAttribute('aria-busy', 'true')
    expect(nav).toHaveClass('sidebar-shell')
  })

  it('keeps the sidebar shell when account or workspace loading fails', () => {
    render(
      <WorkspaceBootShell sidebarLabel="Loading account navigation">
        <ErrorState retry={() => undefined} />
      </WorkspaceBootShell>,
    )
    expect(screen.getByRole('navigation', { name: 'Loading account navigation' })).toBeInTheDocument()
    expect(screen.getByText('Unable to load workspace')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })
})
