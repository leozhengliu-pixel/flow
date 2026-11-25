import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, Release, ReleasePipeline } from '@/types/flow'
import { ReleaseDetailPage } from './release-detail-page'

vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), recordRecentResource: vi.fn(async () => {}), listIssueRecords: vi.fn(async () => ({ issues: [], total: 0 })) }))
vi.mock('@/lib/route-pages', () => ({ AgentChatPanel: () => null }))

const pipeline = { id: 'pipeline-1', slugId: 'app', name: 'App', teamIds: [], type: 'scheduled', production: true, stages: ['Planned', 'Released'], stageStatuses: { Planned: 'planned', Released: 'released' }, position: 0, pathFilters: [], createdAt: '', updatedAt: '' } as unknown as ReleasePipeline
const release = { id: 'release-1', slugId: 'one', name: 'One', pipelineId: 'pipeline-1', stage: 'Planned', status: 'planned', issueIds: [], resources: [], releaseNotes: 'Old notes', createdAt: '', updatedAt: '' } as unknown as Release

function page(data: BootstrapData) {
  return <MemoryRouter><I18nProvider><ReleaseDetailPage data={data} pipeline={pipeline} release={release} tab="release-notes" onNavigate={vi.fn()} onOpenSidebar={vi.fn()} onReload={vi.fn(async () => {})}/></I18nProvider></MemoryRouter>
}

beforeEach(() => localStorage.setItem('flow:locale', 'en'))

describe('release notes painted from cached data', () => {
  it('shows the note the server confirms until the viewer edits it', () => {
    const cached = makeBootstrap({ favorites: [], releasePipelines: [pipeline], releases: [release], releaseNotes: [{ id: 'note-1', releaseId: 'release-1', title: 'Cached title', body: 'Cached body' }] } as unknown as Partial<BootstrapData>)
    const view = render(page(cached))
    expect(screen.getByLabelText('Release notes title')).toHaveValue('Cached title')
    view.rerender(page({ ...cached, releaseNotes: [{ id: 'note-1', releaseId: 'release-1', title: 'Fresh title', body: 'Fresh body' }] } as unknown as BootstrapData))
    expect(screen.getByLabelText('Release notes title')).toHaveValue('Fresh title')
    expect(screen.getByLabelText('Release notes')).toHaveTextContent('Fresh body')
  })
})
