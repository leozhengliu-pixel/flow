import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { toast } from 'sonner'

import { I18nProvider } from '@/i18n/i18n'
import type { ReleasePipeline } from '@/types/flow'

import { PipelineStageEditor } from './pipeline-stage-editor'
import { DEFAULT_STAGES, stageMutation, stagesFromPipeline, type StageDraft } from './pipeline-stages'

vi.mock('sonner', () => ({ toast: { warning: vi.fn(), error: vi.fn(), success: vi.fn(), info: vi.fn() } }))

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.setItem('flow:locale', 'en-US')
})

function Harness({ initial, releaseCountByStage = {}, onChange }: { initial: StageDraft[]; releaseCountByStage?: Record<string, number>; onChange?: (stages: StageDraft[]) => void }) {
  const [stages, setStages] = useState(initial)
  return <I18nProvider><PipelineStageEditor stages={stages} releaseCountByStage={releaseCountByStage} onChange={next => { setStages(next); onChange?.(next) }}/></I18nProvider>
}

const rowNames = () => screen.getAllByRole('listitem').map(row => row.textContent?.replace(/Frozen$/, '').trim())

describe('PipelineStageEditor', () => {
  it('groups default stages like Linear: Planned, a Started group, Released, Canceled', () => {
    render(<Harness initial={DEFAULT_STAGES}/>)
    expect(rowNames()).toEqual(['Planned', 'In Progress', 'Released', 'Canceled'])
    const started = screen.getByRole('group', { name: 'Started' })
    expect(within(started).getByText('Started')).toBeVisible()
    expect(within(started).getByRole('button', { name: 'Create new release stage' })).toBeVisible()
    // Only started stages have a menu; there is no help icon.
    expect(screen.getAllByRole('button', { name: 'Open menu' })).toHaveLength(1)
  })

  it('creates, renames and recolors started stages', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Harness initial={DEFAULT_STAGES} onChange={onChange}/>)
    await user.click(screen.getByRole('button', { name: 'Create new release stage' }))
    const submit = screen.getByRole('button', { name: 'Submit' })
    expect(submit).toHaveTextContent('Create')
    expect(submit).toBeDisabled()
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'in progress')
    expect(submit).toBeDisabled()
    await user.clear(screen.getByRole('textbox', { name: 'Name' }))
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'QA{Enter}')
    expect(rowNames()).toEqual(['Planned', 'In Progress', 'QA', 'Released', 'Canceled'])

    const qa = screen.getAllByRole('listitem')[2]
    await user.click(within(qa).getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }))
    expect(screen.getByRole('button', { name: 'Submit' })).toHaveTextContent('Save')
    await user.click(screen.getByRole('button', { name: 'Stage color' }))
    await user.click(await screen.findByRole('button', { name: 'Teal' }))
    await user.clear(screen.getByRole('textbox', { name: 'Name' }))
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Testing')
    await user.click(screen.getByRole('button', { name: 'Submit' }))
    const last = onChange.mock.calls.at(-1)![0] as StageDraft[]
    expect(last.find(stage => stage.name === 'Testing')).toMatchObject({ status: 'inProgress', color: '#26b5ce', frozen: false })

    // Escape cancels editing without changes.
    await user.click(screen.getByRole('button', { name: 'Create new release stage' }))
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Draft{Escape}')
    expect(rowNames()).toEqual(['Planned', 'In Progress', 'Testing', 'Released', 'Canceled'])
  })

  it('freezes stages but keeps one started stage open, and guards deletes', async () => {
    const user = userEvent.setup()
    const initial = stagesFromPipeline({ stages: ['Planned', 'In Progress', 'QA', 'Released', 'Canceled'], stageStatuses: { Planned: 'planned', 'In Progress': 'inProgress', QA: 'inProgress', Released: 'released', Canceled: 'canceled' }, frozenStages: ['QA'] } as unknown as ReleasePipeline)
    render(<Harness initial={initial} releaseCountByStage={{ QA: 2 }}/>)
    expect(screen.getByText('Frozen')).toBeVisible()
    const inProgress = screen.getAllByRole('listitem')[1]
    await user.click(within(inProgress).getByRole('button', { name: 'Open menu' }))
    expect(await screen.findByRole('menuitem', { name: 'Freeze' })).toHaveAttribute('data-disabled')
    await user.keyboard('{Escape}')

    const qa = screen.getAllByRole('listitem')[2]
    await user.click(within(qa).getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    expect(toast.warning).toHaveBeenCalledWith('Can’t archive the "QA" release stage', { description: 'This stage has releases associated with it. You can edit this stage, or move the releases to a different stage prior to archiving it.' })
    expect(rowNames()).toContain('QA')

    await user.click(within(screen.getAllByRole('listitem')[2]).getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Unfreeze' }))
    expect(screen.queryByText('Frozen')).toBeNull()
  })

  it('refuses to delete the last started stage', async () => {
    const user = userEvent.setup()
    render(<Harness initial={DEFAULT_STAGES}/>)
    await user.click(screen.getByRole('button', { name: 'Open menu' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))
    expect(toast.warning).toHaveBeenCalledWith('Can’t delete the "In Progress" release stage', { description: 'Each type of release stage must have at least one option. You can edit this stage, or create a replacement stage prior to deleting it.' })
    expect(rowNames()).toContain('In Progress')
  })
})

describe('stageMutation', () => {
  it('serializes grouped stages, colors, frozen stages and renames', () => {
    const stages = stagesFromPipeline({ stages: ['Released', 'Planned', 'QA', 'Canceled'], stageStatuses: { Planned: 'planned', QA: 'inProgress', Released: 'released', Canceled: 'canceled' }, stageColors: { QA: '#26b5ce' }, frozenStages: [] } as unknown as ReleasePipeline)
    const renamed = stages.map(stage => stage.name === 'QA' ? { ...stage, name: 'Testing', frozen: true } : stage)
    expect(stageMutation(renamed)).toEqual({
      stages: ['Planned', 'Testing', 'Released', 'Canceled'],
      stageStatuses: { Planned: 'planned', Testing: 'inProgress', Released: 'released', Canceled: 'canceled' },
      stageColors: { Testing: '#26b5ce' },
      frozenStages: ['Testing'],
      stageRenames: { QA: 'Testing' },
    })
  })
})
