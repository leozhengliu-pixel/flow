import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import type { BootstrapData, Release, ReleasePipeline } from '@/types/flow'
import { IssueReleasePicker } from './issue-release-picker'

const issue = makeIssue()
const pipeline = (n: number) => ({ id: `pipe-${n}`, slugId: `pipe-${n}`, name: `Pipeline ${n}`, type: 'scheduled', teamIds: [], stages: ['Planned'], stageStatuses: { Planned: 'planned' } }) as unknown as ReleasePipeline
const release = (id: string, pipelineId: string): Release => ({ id, slugId: id, name: `Release ${id}`, version: '', status: 'planned', pipelineId, issueIds: [issue.id], stage: 'Planned', updatedAt: '2026-09-01T00:00:00Z', createdAt: '2026-09-01T00:00:00Z' }) as unknown as Release

function setup(pipelines: ReleasePipeline[], releases: Release[]) {
  const data = makeBootstrap({ issues: [issue], releasePipelines: pipelines, releases } as Partial<BootstrapData>)
  render(<MemoryRouter><I18nProvider><IssueReleasePicker data={data} issue={issue} grouped/></I18nProvider></MemoryRouter>)
}

beforeEach(() => {
  localStorage.setItem('flow:locale', 'en-US')
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
})

it('shows each release of a different pipeline on its own row with an open link', () => {
  setup([pipeline(1), pipeline(2)], [release('a', 'pipe-1'), release('b', 'pipe-2')])
  expect(document.querySelectorAll('.issue-release-pill')).toHaveLength(2)
  expect(screen.getAllByRole('link', { name: 'Open release' })).toHaveLength(2)
})

it('groups releases of one pipeline into an "N releases" row (Linear)', () => {
  setup([pipeline(1)], [release('a', 'pipe-1'), release('b', 'pipe-1')])
  expect(document.querySelectorAll('.issue-release-pill')).toHaveLength(1)
  expect(screen.getByText('2 releases')).toBeVisible()
  expect(screen.queryByRole('link', { name: 'Open release' })).not.toBeInTheDocument()
})

it('shows three rows and hides the rest behind Show more', async () => {
  const pipelines = [1, 2, 3, 4, 5].map(pipeline)
  setup(pipelines, pipelines.map((item, index) => release(String(index), item.id)))
  expect(document.querySelectorAll('.issue-release-pill')).toHaveLength(3)
  await userEvent.setup().click(screen.getByRole('button', { name: 'Show 2 more' }))
  expect(document.querySelectorAll('.issue-release-pill')).toHaveLength(5)
  await userEvent.setup().click(screen.getByRole('button', { name: 'Show less' }))
  expect(document.querySelectorAll('.issue-release-pill')).toHaveLength(3)
})

it('does not collapse four rows', () => {
  const pipelines = [1, 2, 3, 4].map(pipeline)
  setup(pipelines, pipelines.map((item, index) => release(String(index), item.id)))
  expect(document.querySelectorAll('.issue-release-pill')).toHaveLength(4)
})
