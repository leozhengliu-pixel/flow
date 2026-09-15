import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  createRelease: vi.fn(),
  updateRelease: vi.fn(),
}))

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { Release, ReleasePipeline } from '@/types/flow'

import { ReleaseEditorDialog } from './release-editor-dialog'

const pipeline = {
  id: 'pipeline-1',
  slugId: 'app',
  name: 'App',
  teamIds: ['team-1'],
  type: 'scheduled',
  production: true,
  stages: ['Planning', 'In progress', 'Released'],
  stageStatuses: {
    Planning: 'planned',
    'In progress': 'inProgress',
    Released: 'released',
  },
  position: 0,
  pathFilters: [],
  autoGenerateReleaseNotes: false,
  createdAt: '2026-01-01T00:00:00.000Z',
  updatedAt: '2026-01-02T00:00:00.000Z',
} as ReleasePipeline

const release = {
  id: 'release-1',
  slugId: 'one',
  name: 'Hotfix',
  version: '1.1.0',
  description: '',
  status: 'inProgress',
  pipelineId: 'pipeline-1',
  stage: 'In progress',
  position: 0,
  projectIds: [],
  issueIds: [],
  subscriberIds: [],
  resources: [],
  creator: viewer,
  createdAt: '2026-01-01T00:00:00.000Z',
  updatedAt: '2026-01-02T00:00:00.000Z',
} as Release

beforeEach(() => {
  localStorage.clear()
  localStorage.setItem('flow:locale', 'en-US')
})

describe('ReleaseEditorDialog stage icons', () => {
  it('renders the current stage status on the pill and menu options', async () => {
    const user = userEvent.setup()
    render(
      <I18nProvider>
        <ReleaseEditorDialog
          data={makeBootstrap({ documents: [], favorites: [], releaseNotes: [] })}
          pipeline={pipeline}
          release={release}
          onClose={vi.fn()}
          onSaved={vi.fn().mockResolvedValue(undefined)}
        />
      </I18nProvider>,
    )

    const trigger = screen.getByRole('button', { name: 'Change release stage' })
    expect(trigger.querySelector('[data-icon="release-status"]')).toHaveAttribute('data-status', 'inProgress')
    expect(trigger.querySelector('.lucide-circle-dashed')).toBeNull()

    await user.click(trigger)
    const items = await screen.findAllByRole('menuitem')
    expect(items.map(item => item.querySelector('[data-icon="release-status"]')?.getAttribute('data-status'))).toEqual([
      'planned',
      'inProgress',
      'released',
    ])
  })
})
