import type { ReactElement } from 'react'
import { render, screen, fireEvent } from '@testing-library/react'
import { I18nProvider } from '@/i18n/i18n'
import { describe, expect, it, vi, afterEach } from 'vitest'
import {
  IssuesSplitViewPage,
  IssueViewSplitPage,
  SplitViewIssueView,
  SplitIssueView,
  IssueDetailsPaneSidebar,
  IssueWidgetAdornments,
  buildIssueWidgetAdornments,
  resolvePullRequestLifecycle,
} from './index'
import type { MyIssuesRowData } from '@/components/my-issues/my-issues-list'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
;(globalThis as unknown as { ResizeObserver: typeof ResizeObserverStub }).ResizeObserver = ResizeObserverStub

afterEach(() => {
  window.localStorage.clear()
})

function renderWithI18n(ui: ReactElement) {
  return render(<I18nProvider>{ui}</I18nProvider>)
}

const sampleIssue: MyIssuesRowData = {
  id: 'issue-1',
  identifier: 'FLOW-1',
  title: 'Wire IssuesSplitViewPage',
  priority: 2,
  state: { id: 's1', name: 'In Progress', type: 'started', color: '#5e6ad2' },
  labels: [{ id: 'l1', name: 'wave8', color: '#eb5757' }],
  assignee: { id: 'u1', name: 'Ada' },
  project: { id: 'p1', name: 'Flow', color: '#26b5ce' },
  customerNames: ['Acme'],
  customerIds: ['c1'],
  relationTypes: ['blocked_by', 'blocks'],
  pullRequestCount: 2,
  pullRequestLifecycle: 'merged',
  blockedByCount: 1,
  blockingCount: 1,
  createdAt: '2026-09-01T00:00:00.000Z',
  updatedAt: '2026-09-02T00:00:00.000Z',
  sortOrder: 1,
}

describe('resolvePullRequestLifecycle', () => {
  it('prefers merged over open/closed', () => {
    expect(
      resolvePullRequestLifecycle([
        { status: 'open', draft: false },
        { status: 'merged', draft: false },
        { status: 'closed', draft: false },
      ]),
    ).toBe('merged')
  })

  it('treats draft open PRs as draft', () => {
    expect(resolvePullRequestLifecycle([{ status: 'open', draft: true }])).toBe('draft')
  })
})

describe('buildIssueWidgetAdornments', () => {
  it('counts blocked/blocking relations and PR lifecycle', () => {
    expect(
      buildIssueWidgetAdornments({
        reviews: [{ status: 'merged', draft: false }],
        relationTypes: ['blocked_by', 'blocks', 'blocks'],
      }),
    ).toEqual({
      pullRequestLifecycle: 'merged',
      pullRequestCount: 1,
      blockedByCount: 1,
      blockingCount: 2,
    })
  })
})

describe('IssuesSplitViewPage', () => {
  it('renders shared SplitView with list and detail', () => {
    renderWithI18n(
      <IssuesSplitViewPage
        list={<div>Issue list</div>}
        detail={<div>Issue detail</div>}
      />,
    )
    expect(screen.getByLabelText('Issues split view')).toBeInTheDocument()
    expect(screen.getByText('Issue list')).toBeInTheDocument()
    expect(screen.getByText('Issue detail')).toBeInTheDocument()
    expect(screen.getByRole('separator', { name: 'Resize list' })).toBeInTheDocument()
  })
})

describe('IssueViewSplitPage / SplitViewIssueView', () => {
  it('shows empty chrome when nothing selected', () => {
    renderWithI18n(<IssueViewSplitPage workspaceSlug="acme" origin={{ type: 'myIssues' }} />)
    expect(screen.getByText('Select an issue to open it here')).toBeInTheDocument()
    expect(screen.getByText('My issues')).toBeInTheDocument()
  })

  it('resolves team origin viewKind via SplitViewIssueView', () => {
    renderWithI18n(
      <SplitViewIssueView
        workspaceSlug="acme"
        origin={{ type: 'teamView', teamKey: 'FLOW', viewKind: 'active' }}
        summary={{ labels: [], priority: [], projects: [] }}
      />,
    )
    expect(screen.getByText('Active')).toBeInTheDocument()
  })
})

describe('SplitIssueView + IssueDetailsPaneSidebar', () => {
  it('renders preview, properties, customer needs, and agent slot', () => {
    renderWithI18n(
      <SplitIssueView
        issue={sampleIssue}
        agentPanel={<div>Entity agent</div>}
        onClose={vi.fn()}
      />,
    )
    expect(screen.getByText('FLOW-1')).toBeInTheDocument()
    expect(screen.getByText('Wire IssuesSplitViewPage')).toBeInTheDocument()
    expect(screen.getByLabelText('Issue details sidebar')).toBeInTheDocument()
    expect(screen.getByText('Entity agent')).toBeInTheDocument()
    expect(screen.getByLabelText('Customer needs')).toBeInTheDocument()
    expect(screen.getByText('Acme')).toBeInTheDocument()
  })

  it('toggles agent panel when host provides open state', () => {
    const onAgentOpenChange = vi.fn()
    const { rerender } = renderWithI18n(
      <IssueDetailsPaneSidebar
        issue={sampleIssue}
        agentPanel={<div>Agent body</div>}
        agentOpen={false}
        onAgentOpenChange={onAgentOpenChange}
      />,
    )
    expect(screen.getByText('Properties')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Open chat' }))
    expect(onAgentOpenChange).toHaveBeenCalledWith(true)

    rerender(
      <I18nProvider>
        <IssueDetailsPaneSidebar
          issue={sampleIssue}
          agentPanel={<div>Agent body</div>}
          agentOpen
          onAgentOpenChange={onAgentOpenChange}
        />
      </I18nProvider>,
    )
    expect(screen.getByText('Agent body')).toBeInTheDocument()
  })
})

describe('IssueWidgetAdornments', () => {
  it('renders PR lifecycle and blocked/blocking titles', () => {
    renderWithI18n(
      <IssueWidgetAdornments
        pullRequestLifecycle="merged"
        pullRequestCount={2}
        blockedByCount={1}
        blockingCount={3}
      />,
    )
    expect(screen.getByLabelText('2 merged PRs')).toBeInTheDocument()
    expect(screen.getByLabelText('Blocked by 1 issue')).toBeInTheDocument()
    expect(screen.getByLabelText('Blocking 3 issues')).toBeInTheDocument()
  })
})
