import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { zhCN } from '@/i18n/translations'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { FlowDocument, Loop, LoopOutputContract } from '@/types/flow'

const api = vi.hoisted(() => ({ analyzeLoopOutputs: vi.fn(), updateLoop: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { LoopContractSummary, LoopOutputContractPanel } from './loop-output-contract'
import { CONTRACT_FALLBACK_NOTICE, contractTargetText, personContract } from './loop-output-contract-model'

const instructions = "在目标团队下创建一个新的 Document，标题格式为'每日总结-YYYY-MM-DD'，将完整的总结内容写入该 Document。每次运行都要创建一个单独的 Document，不要复用已有 Document。"

const modelContract: LoopOutputContract = {
  source: 'model',
  readOnly: false,
  outputs: [
    { kind: 'document', operation: 'create', required: true, target: { mode: 'new', titleTemplate: '每日总结-{date}' }, cardinality: 'onePerRun', evidence: "标题格式为'每日总结-YYYY-MM-DD'" },
    { kind: 'comment', operation: 'forbidden', required: false, target: { mode: 'any' }, evidence: '不要发布评论' },
  ],
}

const loop: Loop = {
  id: 'loop-1', name: '每日总结', status: 'published', level: 'team', teamId: 'team-1', triggerType: 'schedule', instructions,
  connectorIds: [], teamAccess: 'allPublic', allowChangesOutsideTrigger: false, allowExternalSync: false, enabled: true, creator: viewer,
  createdAt: '2026-10-28T00:00:00Z', updatedAt: '2026-10-28T00:00:00Z',
}

function renderPanel(current: Loop, unsaved = false) {
  const onLoopChange = vi.fn()
  const data = makeBootstrap()
  const view = render(<I18nProvider><LoopOutputContractPanel data={data} loop={current} unsaved={unsaved} onLoopChange={onLoopChange} readDelayMs={0}/></I18nProvider>)
  return { onLoopChange, view, data }
}

describe('LoopOutputContractPanel', () => {
  beforeEach(() => {
    for (const mock of Object.values(api)) mock.mockReset()
    api.analyzeLoopOutputs.mockResolvedValue({ ...loop, outputContract: modelContract })
    api.updateLoop.mockImplementation(async (id: string, input: Partial<Loop>) => ({ ...loop, id, outputContract: input.outputContract ?? undefined }))
  })

  it('reads the contract of saved instructions once and shows each output with its evidence', async () => {
    const { onLoopChange } = renderPanel(loop)
    await waitFor(() => expect(api.analyzeLoopOutputs).toHaveBeenCalledWith('loop-1'))
    expect(onLoopChange).toHaveBeenCalledWith(expect.objectContaining({ outputContract: modelContract }))
    const { view } = renderPanel({ ...loop, outputContract: modelContract })
    const list = within(view.container).getByRole('list', { name: 'Outputs' })
    const items = within(list).getAllByRole('listitem')
    expect(items[0]).toHaveTextContent('CreateDocumentMust produce')
    expect(items[0]).toHaveTextContent('titled 每日总结-{date}')
    expect(items[0]).toHaveTextContent('One per run')
    expect(items[0]).toHaveTextContent("标题格式为'每日总结-YYYY-MM-DD'")
    expect(items[1]).toHaveTextContent('NeverComment')
    // A fresh contract is not read again.
    expect(api.analyzeLoopOutputs).toHaveBeenCalledTimes(1)
  })

  it('waits for unsaved instructions and re-reads a stale contract', async () => {
    renderPanel({ ...loop, outputContract: { ...modelContract, stale: true } }, true)
    expect(screen.getByText('Save the loop to read its outputs from the new instructions.')).toBeVisible()
    await new Promise(resolve => setTimeout(resolve, 10))
    expect(api.analyzeLoopOutputs).not.toHaveBeenCalled()
    renderPanel({ ...loop, outputContract: { ...modelContract, stale: true } })
    await waitFor(() => expect(api.analyzeLoopOutputs).toHaveBeenCalledTimes(1))
  })

  it('warns when the outputs could not be determined', () => {
    renderPanel({ ...loop, outputContract: { source: 'fallback', readOnly: true, outputs: [], error: CONTRACT_FALLBACK_NOTICE, extractedAt: '2026-10-28T00:00:00Z' } })
    expect(screen.getByRole('alert')).toHaveTextContent(CONTRACT_FALLBACK_NOTICE)
    expect(api.analyzeLoopOutputs).not.toHaveBeenCalled()
  })

  it('saves a person\'s edits as the authoritative contract and can go back to the automatic one', async () => {
    const { onLoopChange, data } = renderPanel({ ...loop, outputContract: modelContract })
    await userEvent.click(screen.getByRole('button', { name: 'Edit outputs' }))
    const rows = screen.getByRole('list', { name: 'Outputs' })
    const title = within(rows).getByRole('textbox', { name: 'Title template' })
    await userEvent.clear(title)
    await userEvent.type(title, 'Daily {{date}')
    await userEvent.click(within(rows).getAllByRole('button', { name: 'Remove output' })[1])
    await userEvent.click(screen.getByRole('button', { name: 'Save outputs' }))
    await waitFor(() => expect(api.updateLoop).toHaveBeenCalledTimes(1))
    const sent = api.updateLoop.mock.calls[0][1].outputContract as LoopOutputContract
    expect(sent.source).toBe('person')
    expect(sent.outputs).toHaveLength(1)
    expect(sent.outputs[0]).toMatchObject({ kind: 'document', operation: 'create', required: true, target: { titleTemplate: 'Daily {date}' } })
    expect(onLoopChange).toHaveBeenCalled()

    const person: Loop = { ...loop, outputContract: { ...sent, stale: true } }
    renderPanel(person)
    expect(screen.getAllByText('Set by a person')[0]).toBeVisible()
    expect(screen.getByText('The instructions changed after these outputs were set. Check that they still fit.')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Use automatic outputs' }))
    await waitFor(() => expect(api.updateLoop).toHaveBeenLastCalledWith('loop-1', { outputContract: null }))
    await waitFor(() => expect(api.analyzeLoopOutputs).toHaveBeenCalled())
    expect(data.teams.length).toBeGreaterThan(0)
  })

  it('picks an existing document for an append and a team for a new document', async () => {
    const document = { id: 'doc-1', title: 'Release log', content: '' } as unknown as FlowDocument
    const data = makeBootstrap({ documents: [document] })
    render(<I18nProvider><LoopOutputContractPanel data={data} loop={{ ...loop, outputContract: { source: 'model', readOnly: false, outputs: [{ kind: 'document', operation: 'append', required: true, target: { mode: 'existing', title: 'Team log' } }] } }} unsaved={false} onLoopChange={vi.fn()} readDelayMs={0}/></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Edit outputs' }))
    await userEvent.click(screen.getByRole('combobox', { name: 'Document' }))
    await userEvent.click(await screen.findByRole('option', { name: document.title }))
    await userEvent.click(screen.getByRole('button', { name: 'Save outputs' }))
    await waitFor(() => expect(api.updateLoop).toHaveBeenCalled())
    expect((api.updateLoop.mock.calls[0][1].outputContract as LoopOutputContract).outputs[0].target).toMatchObject({ documentId: document.id, title: document.title, mode: 'existing' })
  })
})

describe('LoopContractSummary', () => {
  it('shows a run\'s resolved contract: the rendered title and the team', () => {
    const data = makeBootstrap()
    const team = data.teams[0]
    render(<I18nProvider><LoopContractSummary data={data} showEvidence={false} contract={{ source: 'model', readOnly: false, outputs: [{ kind: 'document', operation: 'create', required: true, target: { mode: 'new', titleTemplate: '每日总结-{date}', renderedTitle: '每日总结-2026-10-28', teamId: team.id }, cardinality: 'onePerRun', evidence: 'x' }] }}/></I18nProvider>)
    const item = screen.getByRole('listitem')
    expect(item).toHaveTextContent(`titled 每日总结-2026-10-28 · in ${team.name} · One per run`)
    expect(item).not.toHaveTextContent('x')
  })

  it('says a reply-only contract changes nothing', () => {
    render(<I18nProvider><LoopContractSummary data={makeBootstrap()} contract={{ source: 'model', readOnly: true, readOnlyEvidence: '只回复总结文本即可', outputs: [{ kind: 'comment', operation: 'forbidden', required: false, target: {} }] }}/></I18nProvider>)
    expect(screen.getByText('Reply only: the run changes nothing')).toBeVisible()
    expect(screen.getByText('只回复总结文本即可')).toBeVisible()
  })
})

describe('contract helpers', () => {
  it('describe targets and strip computed fields', () => {
    const data = makeBootstrap()
    const t = (source: string) => source
    expect(contractTargetText({ kind: 'document', operation: 'append', required: true, target: { mode: 'existing', title: 'Release log' } }, data, t)).toBe('the document Release log')
    expect(contractTargetText({ kind: 'issue', operation: 'create', required: false, target: { mode: 'new' }, cardinality: 'perItem', conditions: ['for every new error'] }, data, t)).toBe('A new one each time · One per item · when for every new error')
    const saved = personContract({ source: 'model', readOnly: true, stale: true, outputs: [{ kind: 'document', operation: 'create', required: true, target: { renderedTitle: 'x' } }, { kind: 'comment', operation: 'forbidden', required: false, target: {} }] })
    expect(saved).toMatchObject({ source: 'person', readOnly: true })
    expect(saved.outputs.map(output => output.kind)).toEqual(['comment'])
    expect('stale' in saved).toBe(false)
  })

  it('has Chinese copy for every panel string', () => {
    for (const key of ['What this loop will produce', 'Outputs this run worked to', CONTRACT_FALLBACK_NOTICE, 'Must produce', 'Edit outputs', 'Save outputs', 'Use automatic outputs', 'Reply only: the run changes nothing', 'Title template', 'titled {title}', 'One per run', 'Create or update', 'Read only', 'Status update', 'Other change']) {
      expect(zhCN[key], key).toBeTruthy()
    }
  })
})
