import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('@/lib/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/api')>()),
  createRelease: vi.fn(),
  updateRelease: vi.fn(),
}))

import { I18nProvider } from '@/i18n/i18n'
import { updateRelease } from '@/lib/api'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { MentionShell, pasteText, stubEditorEnvironment } from '@/test/mention-host-harness'
import { mentionFixture, mentionUrls } from '@/components/editor/mentions/mention-fixtures'
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
  stubEditorEnvironment()
})
afterEach(() => { vi.unstubAllGlobals() })

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

describe('ReleaseEditorDialog description mentions', () => {
  it('saves a description with a mention and a pasted Flow URL, and shows the chip when reopened', async () => {
    const user = userEvent.setup()
    const data = mentionFixture()
    const onSaved = vi.fn().mockResolvedValue(undefined)
    vi.mocked(updateRelease).mockResolvedValue(undefined as never)
    const first = render(<MentionShell data={data}><ReleaseEditorDialog data={data} pipeline={pipeline} release={release} onClose={vi.fn()} onSaved={onSaved}/></MentionShell>)

    const box = await screen.findByRole('textbox', { name: 'Release description' })
    await user.click(box)
    // The dialog is modal (the page behind it is inert), so the "@" option is picked with the keyboard.
    await user.keyboard('Notes: @Launch')
    await screen.findByRole('option', { name: /Launch plan/ })
    await user.keyboard('{Enter}')
    pasteText(box, `${window.location.origin}${mentionUrls.project}`)
    await waitFor(() => expect(box.querySelector('a[data-agent-entity="project"]')).toHaveTextContent('Project one'))
    await user.keyboard('{Control>}{Enter}{/Control}')

    await waitFor(() => expect(updateRelease).toHaveBeenCalled())
    const input = vi.mocked(updateRelease).mock.calls[0][1] as { description: string }
    expect(input.description).toContain('[Launch plan](/workspace/document/plan-abc)')
    expect(input.description).toContain(`[Project one](${mentionUrls.project})`)
    first.unmount()

    render(<MentionShell data={data}><ReleaseEditorDialog data={data} pipeline={pipeline} release={{ ...release, description: input.description }} onClose={vi.fn()} onSaved={onSaved}/></MentionShell>)
    const reopened = await screen.findByRole('textbox', { name: 'Release description' })
    await waitFor(() => expect(reopened.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
  })
})
