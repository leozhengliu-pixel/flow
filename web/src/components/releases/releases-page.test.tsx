import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.hoisted(() => {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }),
  })
})

const api = vi.hoisted(() => ({
  listIssueRecords: vi.fn().mockResolvedValue({ items: [], hasMore: false, total: 0 }),
  updateRelease: vi.fn().mockResolvedValue({}),
  deleteRelease: vi.fn().mockResolvedValue({}),
  recordRecentResource: vi.fn().mockResolvedValue({}),
}))

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  ...api,
}))

vi.mock('@/lib/favorites', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/favorites')>()),
  toggleFavoriteFor: vi.fn(),
}))

import { I18nProvider } from '@/i18n/i18n'
import { backlog, completed, makeBootstrap, makeIssue, started, viewer } from '@/test/fixtures'
import type { Release, ReleasePipeline } from '@/types/flow'

import { ReleasesPage } from './releases-page'

function pipeline(overrides: Partial<ReleasePipeline> = {}): ReleasePipeline {
  return {
    id: 'pipeline-1',
    slugId: 'app',
    name: 'App',
    teamIds: ['team-1'],
    type: 'scheduled',
    production: true,
    stages: ['Planning', 'In progress', 'Released', 'Canceled'],
    stageStatuses: {
      Planning: 'planned',
      'In progress': 'inProgress',
      Released: 'released',
      Canceled: 'canceled',
    },
    position: 0,
    pathFilters: [],
    autoGenerateReleaseNotes: false,
    createdAt: '2026-01-01T00:00:00.000Z',
    updatedAt: '2026-01-02T00:00:00.000Z',
    ...overrides,
  }
}

function release(overrides: Partial<Release> = {}): Release {
  return {
    id: 'release-1',
    slugId: 'one',
    name: '1.0 Planned',
    version: '1.0.0',
    description: 'First ship',
    status: 'planned',
    pipelineId: 'pipeline-1',
    stage: 'Planning',
    position: 0,
    projectIds: [],
    issueIds: [],
    subscriberIds: [],
    resources: [],
    creator: viewer,
    targetDate: '2026-09-20',
    createdAt: '2026-01-01T00:00:00.000Z',
    updatedAt: '2026-01-02T00:00:00.000Z',
    ...overrides,
  } as Release
}

function renderPage(node: ReactNode) {
  return render(<I18nProvider>{node}</I18nProvider>)
}

function pageData(overrides: Parameters<typeof makeBootstrap>[0] = {}) {
  return makeBootstrap({
    documents: [],
    favorites: [],
    releaseNotes: [],
    ...overrides,
  })
}

beforeEach(() => {
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
  api.listIssueRecords.mockResolvedValue({ items: [], hasMore: false, total: 0 })
})

describe('ReleasesPage icons', () => {
  it('renders pipeline rows with the pipeline icon instead of a rocket', () => {
    const { container } = renderPage(
      <ReleasesPage
        data={pageData({ releasePipelines: [pipeline()] })}
        onNavigate={vi.fn()}
        onOpenSidebar={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
      />,
    )

    const row = container.querySelector('.flow-pipeline-row')
    expect(row?.querySelector('[data-icon="release-pipeline"]')).toBeTruthy()
    expect(container.querySelector('.lucide-rocket')).toBeNull()
    expect(screen.getByRole('button', { name: /App/ })).toBeVisible()
  })

  it('uses an empty-state pipeline icon when no pipelines exist', () => {
    const { container } = renderPage(
      <ReleasesPage
        data={pageData({ releasePipelines: [] })}
        onNavigate={vi.fn()}
        onOpenSidebar={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
      />,
    )

    expect(screen.getByText('No release pipelines')).toBeVisible()
    expect(container.querySelector('.flow-release-empty [data-icon="release-pipeline"]')).toBeTruthy()
    expect(container.querySelector('.lucide-rocket')).toBeNull()
  })

  it('renders status-aware icons on release rows and stage headers', () => {
    const releases = [
      release(),
      release({ id: 'release-2', slugId: 'two', name: '1.1 Shipping', version: '1.1.0', status: 'inProgress', stage: 'In progress', position: 1 }),
      release({ id: 'release-3', slugId: 'three', name: '1.2 Released', version: '1.2.0', status: 'released', stage: 'Released', position: 2 }),
      release({ id: 'release-4', slugId: 'four', name: '0.9 Canceled', version: '0.9.0', status: 'canceled', stage: 'Canceled', position: 3 }),
    ]
    const { container } = renderPage(
      <ReleasesPage
        data={pageData({ releasePipelines: [pipeline()], releases })}
        pipelineSlug="app"
        onNavigate={vi.fn()}
        onOpenSidebar={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
      />,
    )

    expect(container.querySelector('.lucide-circle-dashed')).toBeNull()
    const rows = [...container.querySelectorAll('.flow-release-row')]
    expect(rows.map(row => row.querySelector('[data-icon="release-status"]')?.getAttribute('data-status'))).toEqual([
      'planned',
      'inProgress',
      'released',
      'canceled',
    ])
    expect(screen.getByText('1.0.0')).toBeVisible()

    const headers = [...container.querySelectorAll('.flow-release-group > header button')]
    expect(headers.map(header => [
      header.querySelector('strong')?.textContent,
      header.querySelector('[data-icon="release-status"]')?.getAttribute('data-status'),
    ])).toEqual([
      ['Planning', 'planned'],
      ['In progress', 'inProgress'],
      ['Released', 'released'],
      ['Canceled', 'canceled'],
    ])
  })

  it('uses the current stage status on the detail sidebar and stage menu', async () => {
    const user = userEvent.setup()
    const item = release({ status: 'inProgress', stage: 'In progress', name: 'Hotfix' })
    renderPage(
      <ReleasesPage
        data={pageData({
          releasePipelines: [pipeline()],
          releases: [item],
          documents: [],
          favorites: [],
        })}
        pipelineSlug="app"
        releaseSlug="one"
        releaseTab="issues"
        onNavigate={vi.fn()}
        onOpenSidebar={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
      />,
    )

    const sidebar = screen.getByRole('complementary', { name: 'Release details' })
    expect(within(sidebar).getByRole('button', { name: /In progress/ }).querySelector('[data-status="inProgress"]')).toBeTruthy()

    await user.click(within(sidebar).getByRole('button', { name: /In progress/ }))
    const menu = await screen.findByRole('menu')
    expect([...menu.querySelectorAll('[data-icon="release-status"]')].map(icon => icon.getAttribute('data-status'))).toEqual([
      'planned',
      'inProgress',
      'released',
      'canceled',
    ])
  })
})

describe('release display select alignment', () => {
  it('keeps every option label left-aligned and renders the check only when selected', async () => {
    const user = userEvent.setup()
    renderPage(
      <ReleasesPage
        data={pageData({ releasePipelines: [pipeline()], releases: [release()] })}
        pipelineSlug="app"
        onNavigate={vi.fn()}
        onOpenSidebar={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
      />,
    )

    await user.click(screen.getByRole('button', { name: 'Display options' }))
    await user.click(screen.getByRole('combobox', { name: 'Ordering' }))
    const listbox = await screen.findByRole('listbox')
    const options = within(listbox).getAllByRole('option')
    for (const option of options) {
      const label = option.querySelector('span:first-child')
      expect(label).not.toHaveClass('flow-pipeline-display-select-indicator')
    }
    expect(options.map(option => option.textContent)).toEqual(['Release date', 'Release', 'Name'])
    expect(options[0].querySelector('.flow-pipeline-display-select-indicator')).toBeTruthy()
  })
})


describe('release detail associated issues', () => {
  it('loads every visible associated issue from the release query instead of the issue cache', async () => {
    const states = [backlog, started, completed]
    const associated = Array.from({ length: 18 }, (_, index) => makeIssue({
      id: `issue-${index + 1}`,
      identifier: `TST-${index + 1}`,
      title: `Release issue ${index + 1}`,
      state: states[index % 3],
    }))
    api.listIssueRecords.mockImplementation(async (query?: { cursor?: string }) => {
      if (query?.cursor) return { items: associated.slice(10), hasMore: false, total: -1 }
      return { items: associated.slice(0, 10), hasMore: true, nextCursor: 'page-2', total: -1 }
    })
    const item = release({ issueIds: associated.map(issue => issue.id) })
    renderPage(
      <ReleasesPage
        data={pageData({
          issueCollectionPaged: true,
          issues: associated.slice(0, 2),
          states: [backlog, started, completed],
          releasePipelines: [pipeline()],
          releases: [item],
        })}
        pipelineSlug="app"
        releaseSlug="one"
        releaseTab="issues"
        onNavigate={vi.fn()}
        onOpenSidebar={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
      />,
    )
    expect(await screen.findByText('Release issue 1')).toBeVisible()
    await waitFor(() => expect(screen.getByText('Release issue 18')).toBeVisible())
    expect(screen.getByText('Release issue 2')).toBeVisible()
    expect(screen.getByText('Release issue 3')).toBeVisible()
    expect(screen.queryByText('No issues in this release yet.')).not.toBeInTheDocument()
    expect(api.listIssueRecords).toHaveBeenCalledWith(expect.objectContaining({ releaseId: 'release-1', archived: 'false', limit: 100 }), expect.any(AbortSignal))
    expect(api.listIssueRecords).toHaveBeenCalledWith(expect.objectContaining({ releaseId: 'release-1', cursor: 'page-2' }), expect.any(AbortSignal))
  })

  it('still loads the associated issues on a cold open with an empty issue cache', async () => {
    const associated = Array.from({ length: 18 }, (_, index) => makeIssue({
      id: `cold-${index + 1}`,
      identifier: `COLD-${index + 1}`,
      title: `Cold issue ${index + 1}`,
      state: index < 6 ? backlog : index < 12 ? started : completed,
    }))
    api.listIssueRecords.mockResolvedValue({ items: associated, hasMore: false, total: 18 })
    renderPage(
      <ReleasesPage
        data={pageData({
          issueCollectionPaged: true,
          issues: [],
          states: [backlog, started, completed],
          releasePipelines: [pipeline()],
          releases: [release({ issueIds: associated.map(issue => issue.id) })],
        })}
        pipelineSlug="app"
        releaseSlug="one"
        releaseTab="issues"
        onNavigate={vi.fn()}
        onOpenSidebar={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
      />,
    )
    await waitFor(() => expect(screen.getByText('Cold issue 18')).toBeVisible())
    expect(screen.getByText('Cold issue 1')).toBeVisible()
    expect(screen.getByText('Cold issue 7')).toBeVisible()
    expect(api.listIssueRecords.mock.calls.every(call => call[0]?.releaseId === 'release-1')).toBe(true)
  })

  it('clears the previous release issues while the next scope is loading', async () => {
    const first = makeIssue({ id: 'first', identifier: 'TST-1', title: 'First release issue' })
    const second = makeIssue({ id: 'second', identifier: 'TST-2', title: 'Second release issue' })
    api.listIssueRecords
      .mockResolvedValueOnce({ items: [first], hasMore: false, total: 1 })
      .mockImplementationOnce(() => new Promise(() => undefined))
    const firstRelease = release({ id: 'release-1', slugId: 'one', issueIds: [first.id] })
    const secondRelease = release({ id: 'release-2', slugId: 'two', issueIds: [second.id] })
    const data = pageData({
      issueCollectionPaged: true,
      issues: [],
      releasePipelines: [pipeline()],
      releases: [firstRelease, secondRelease],
    })
    const { rerender } = renderPage(
      <ReleasesPage
        data={data}
        pipelineSlug="app"
        releaseSlug="one"
        releaseTab="issues"
        onNavigate={vi.fn()}
        onOpenSidebar={vi.fn()}
        onReload={vi.fn().mockResolvedValue(undefined)}
      />,
    )
    expect(await screen.findByText(first.title)).toBeVisible()

    rerender(
      <I18nProvider>
        <ReleasesPage
          data={data}
          pipelineSlug="app"
          releaseSlug="two"
          releaseTab="issues"
          onNavigate={vi.fn()}
          onOpenSidebar={vi.fn()}
          onReload={vi.fn().mockResolvedValue(undefined)}
        />
      </I18nProvider>,
    )

    await waitFor(() => expect(screen.queryByText(first.title)).not.toBeInTheDocument())
    expect(screen.getAllByText('Loading issues').length).toBeGreaterThan(0)
  })
})
