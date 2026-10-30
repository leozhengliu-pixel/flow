import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { zhCN } from '@/i18n/translations'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { FlowDocument, Loop, LoopOutputContract } from '@/types/flow'

const api = vi.hoisted(() => ({ analyzeLoopOutputs: vi.fn(), updateLoop: vi.fn(), searchDocuments: vi.fn(), createDocument: vi.fn() }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))

import { LoopContractSummary, LoopOutputContractPanel } from './loop-output-contract'
import { LoopContractReview } from './loop-contract-review'
import { LoopDocumentPicker } from './loop-document-picker'
import { CONTRACT_FALLBACK_NOTICE, contractOutputSummary, contractTargetText, personContract } from './loop-output-contract-model'

beforeEach(() => {
  // The document picker is a cmdk list.
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  Element.prototype.scrollIntoView ??= () => undefined
})

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
    expect(screen.getByText('Instructions changed — review suggested outputs')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: 'Use automatic outputs' }))
    await waitFor(() => expect(api.updateLoop).toHaveBeenLastCalledWith('loop-1', { outputContract: null }))
    await waitFor(() => expect(api.analyzeLoopOutputs).toHaveBeenCalled())
    expect(data.teams.length).toBeGreaterThan(0)
  })

  it('picks an existing document for an append from the server search', async () => {
    const document = { id: 'doc-1', title: 'Release log', content: '', teamIds: ['team-1'] } as unknown as FlowDocument
    api.searchDocuments.mockResolvedValue({ nodes: [document], pageInfo: { hasNextPage: false } })
    // The bootstrap does not hold the document: the picker reads it from the server.
    const data = makeBootstrap({ documents: [] })
    render(<I18nProvider><LoopOutputContractPanel data={data} loop={{ ...loop, outputContract: { source: 'model', readOnly: false, outputs: [{ kind: 'document', operation: 'append', required: true, target: { mode: 'existing', title: 'Team log' } }] } }} unsaved={false} onLoopChange={vi.fn()} readDelayMs={0}/></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Edit outputs' }))
    await userEvent.click(screen.getByRole('button', { name: 'Document' }))
    await userEvent.click(await screen.findByRole('option', { name: /Release log/ }))
    expect(api.searchDocuments).toHaveBeenCalledWith({ q: '', teamId: 'team-1' }, expect.anything())
    await userEvent.click(screen.getByRole('button', { name: 'Save outputs' }))
    await waitFor(() => expect(api.updateLoop).toHaveBeenCalled())
    expect((api.updateLoop.mock.calls[0][1].outputContract as LoopOutputContract).outputs[0].target).toMatchObject({ documentId: document.id, title: document.title, mode: 'existing' })
  })

  it('reads changed instructions without replacing person outputs and reviews each output', async () => {
    const current: LoopOutputContract = { source: 'person', stale: true, readOnly: false, outputs: [{ kind: 'issue', operation: 'update', required: false, target: { mode: 'matching', match: 'customer bugs' }, cardinality: 'any' }] }
    const suggested: LoopOutputContract = { source: 'model', instructionsHash: 'new-text', readOnly: false, outputs: [{ kind: 'issue', operation: 'update', required: true, target: { mode: 'matching', match: 'customer bugs without repro steps' }, cardinality: 'any' }] }
    api.analyzeLoopOutputs.mockResolvedValue({ ...loop, outputContract: current, suggestedOutputContract: suggested })
    const { onLoopChange, view, data } = renderPanel({ ...loop, outputContract: current })
    expect(screen.getByText('Set by a person')).toBeVisible()
    await waitFor(() => expect(onLoopChange).toHaveBeenCalledWith(expect.objectContaining({ outputContract: current, suggestedOutputContract: suggested })))
    view.rerender(<I18nProvider><LoopOutputContractPanel data={data} loop={{ ...loop, outputContract: current, suggestedOutputContract: suggested }} unsaved={false} onLoopChange={onLoopChange} readDelayMs={0}/></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Review suggested outputs' }))
    const output = screen.getByRole('group', { name: 'Output 1' })
    expect(output).toHaveTextContent('Update · Issue · issues matching customer bugs')
    expect(output).toHaveTextContent('Update · Issue · issues matching customer bugs without repro steps')
    await userEvent.click(within(output).getByRole('button', { name: 'Accept suggestion' }))
    await userEvent.click(screen.getByRole('button', { name: 'Save review' }))
    await waitFor(() => expect(api.updateLoop).toHaveBeenCalledTimes(1))
    expect(api.updateLoop.mock.calls[0][1].outputContract.outputs[0]).toMatchObject({ required: true, target: { match: 'customer bugs without repro steps' } })
  })

  it('keeps a person contract without accepting any suggested fields', async () => {
    const current: LoopOutputContract = { source: 'person', stale: true, readOnly: true, outputs: [] }
    renderPanel({ ...loop, outputContract: current, suggestedOutputContract: modelContract })
    await userEvent.click(screen.getByRole('button', { name: 'Keep my outputs' }))
    await waitFor(() => expect(api.updateLoop).toHaveBeenCalledTimes(1))
    expect(api.updateLoop.mock.calls[0][1].outputContract).toMatchObject({ readOnly: true, outputs: [] })
    expect(api.analyzeLoopOutputs).not.toHaveBeenCalled()
  })

  it('renders stale review values as human-readable targets, not ids or enum keys', () => {
    const documentId = 'document_1793_internal'
    const data = makeBootstrap({ documents: [{ id: documentId, title: '团队日报汇总', content: '', teamIds: [] }] as never })
    const current: LoopOutputContract = { source: 'person', readOnly: false, outputs: [{ kind: 'document', operation: 'append', required: true, target: { mode: 'existing', documentId, title: '团队日报汇总' }, cardinality: 'onePerRun' }] }
    const suggested: LoopOutputContract = { source: 'model', readOnly: false, outputs: [{ kind: 'document', operation: 'append', required: true, target: { mode: 'existing', ambiguous: true, description: 'the team document' }, cardinality: 'onePerRun' }] }
    const { container } = render(<I18nProvider><LoopContractReview data={data} current={current} suggested={suggested} saving={false} onSave={vi.fn()} onCancel={vi.fn()} /></I18nProvider>)
    expect(container).toHaveTextContent('团队日报汇总')
    expect(container).toHaveTextContent('Choose an existing document')
    expect(container).not.toHaveTextContent(documentId)
    expect(container).not.toHaveTextContent('Document not chosen')
  })

  it('warns about a document the instructions do not name and saves the one picked', async () => {
    const document = { id: 'doc-7', title: 'Team digest', content: '', teamIds: [] } as unknown as FlowDocument
    api.searchDocuments.mockResolvedValue({ nodes: [document], pageInfo: { hasNextPage: false } })
    const vague: LoopOutputContract = { source: 'model', readOnly: false, outputs: [{ kind: 'document', operation: 'append', required: true, target: { mode: 'existing', ambiguous: true, description: 'the team document' }, evidence: '追加到团队文档' }] }
    renderPanel({ ...loop, outputContract: vague })
    const warning = screen.getAllByRole('alert')[0]
    expect(warning).toHaveTextContent("The instructions don't say which document to write to.")
    expect(within(warning).getByRole('button', { name: 'Document' })).toHaveClass('loops-contract-document')
    expect(screen.getByRole('listitem')).toHaveTextContent('document not chosen yet')
    await userEvent.click(screen.getByRole('button', { name: 'Document' }))
    await userEvent.click(await screen.findByRole('option', { name: /Team digest/ }))
    await waitFor(() => expect(api.updateLoop).toHaveBeenCalledTimes(1))
    const sent = api.updateLoop.mock.calls[0][1].outputContract as LoopOutputContract
    expect(sent.source).toBe('person')
    expect(sent.outputs[0].target).toMatchObject({ documentId: 'doc-7', title: 'Team digest', ambiguous: false })
  })

  it('keeps an unnamed document unsaved until it is chosen, or a new one is created', async () => {
    api.searchDocuments.mockResolvedValue({ nodes: [], pageInfo: { hasNextPage: false } })
    api.createDocument.mockResolvedValue({ id: 'doc-new', title: 'Weekly notes', teamIds: ['team-1'] })
    renderPanel({ ...loop, outputContract: { source: 'model', readOnly: false, outputs: [{ kind: 'document', operation: 'update', required: true, target: { mode: 'existing', ambiguous: true, description: 'the team doc' } }] } })
    await userEvent.click(screen.getByRole('button', { name: 'Edit outputs' }))
    expect(screen.getByRole('button', { name: 'Save outputs' })).toBeDisabled()
    expect(screen.getByText('Choose the document for each output that writes to an existing one.')).toBeVisible()
    const row = screen.getByRole('list', { name: 'Outputs' })
    await userEvent.click(within(row).getByRole('button', { name: 'Document' }))
    await userEvent.type(await screen.findByPlaceholderText('Search documents…'), 'Weekly notes')
    await userEvent.click(await screen.findByRole('option', { name: /Create a new document “Weekly notes”/ }))
    await waitFor(() => expect(api.createDocument).toHaveBeenCalledWith({ title: 'Weekly notes', teamIds: ['team-1'] }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save outputs' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: 'Save outputs' }))
    await waitFor(() => expect(api.updateLoop).toHaveBeenCalled())
    expect((api.updateLoop.mock.calls[0][1].outputContract as LoopOutputContract).outputs[0].target).toMatchObject({ documentId: 'doc-new', ambiguous: false })
  })

  it('sets where comments, status updates and new issues go, and what fills a title placeholder', async () => {
    const initiative = { id: 'init-1', name: 'Growth push' }
    const data = makeBootstrap({ initiatives: [initiative] } as never)
    const project = data.projects[0]
    render(<I18nProvider><LoopOutputContractPanel data={data} loop={{ ...loop, outputContract: { source: 'model', readOnly: false, outputs: [
      { kind: 'comment', operation: 'create', required: true, target: { mode: 'any' } },
      { kind: 'statusUpdate', operation: 'create', required: true, target: { mode: 'any' } },
      { kind: 'issue', operation: 'create', required: false, target: { mode: 'new' } },
    ] } }} unsaved={false} onLoopChange={vi.fn()} readDelayMs={0}/></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Edit outputs' }))
    const rows = within(screen.getByRole('list', { name: 'Outputs' })).getAllByRole('listitem')
    await userEvent.click(within(rows[0]).getByRole('combobox', { name: 'Target' }))
    await userEvent.click(await screen.findByRole('option', { name: 'The triggering item' }))
    await userEvent.click(within(rows[1]).getByRole('combobox', { name: 'Target' }))
    await userEvent.click(await screen.findByRole('option', { name: 'An initiative' }))
    await userEvent.click(within(rows[1]).getByRole('combobox', { name: 'Initiative' }))
    await userEvent.click(await screen.findByRole('option', { name: 'Growth push' }))
    expect(within(rows[2]).queryByRole('combobox', { name: 'Target' })).toBeNull()
    await userEvent.click(within(rows[2]).getByRole('combobox', { name: 'Project' }))
    await userEvent.click(await screen.findByRole('option', { name: project.name }))
    await userEvent.type(within(rows[2]).getByRole('textbox', { name: 'Title template' }), 'Error: {{message} {{date}')
    await userEvent.type(within(rows[2]).getByRole('textbox', { name: 'What fills {message}' }), "the error's message")
    expect(within(rows[2]).queryByRole('textbox', { name: 'What fills {date}' })).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Save outputs' }))
    await waitFor(() => expect(api.updateLoop).toHaveBeenCalled())
    const outputs = (api.updateLoop.mock.calls[0][1].outputContract as LoopOutputContract).outputs
    expect(outputs[0].target).toMatchObject({ mode: 'triggering' })
    expect(outputs[1].target).toMatchObject({ mode: 'existing', initiativeId: 'init-1', initiative: 'Growth push' })
    expect(outputs[2].target).toMatchObject({ projectId: project.id, titleTemplate: 'Error: {message} {date}', placeholders: [{ name: 'message', source: "the error's message" }] })
  })
})

describe('LoopDocumentPicker', () => {
  beforeEach(() => {
    for (const mock of Object.values(api)) mock.mockReset()
  })

  it('searches on the server by title and team, pages, and keeps recent first', async () => {
    const page = (ids: string[], more: boolean) => ({ nodes: ids.map(id => ({ id, title: `Doc ${id}`, teamIds: ['team-1'] })), pageInfo: { hasNextPage: more, endCursor: more ? `cursor-${ids.at(-1)}` : null } })
    api.searchDocuments.mockImplementation(async (options: { q?: string; after?: string }) => options.after ? page(['c'], false) : options.q ? page(['q'], false) : page(['a', 'b'], true))
    const onSelect = vi.fn()
    const data = makeBootstrap()
    render(<I18nProvider><LoopDocumentPicker data={data} open teamId="team-1" onOpenChange={vi.fn()} onSelect={onSelect}/></I18nProvider>)
    expect(await screen.findByRole('option', { name: /Doc a/ })).toBeVisible()
    expect(screen.getByText('Recent documents')).toBeVisible()
    await userEvent.click(screen.getByRole('option', { name: 'Load more' }))
    expect(await screen.findByRole('option', { name: /Doc c/ })).toBeVisible()
    expect(api.searchDocuments).toHaveBeenLastCalledWith({ q: '', teamId: 'team-1', after: 'cursor-b' })
    await userEvent.type(screen.getByPlaceholderText('Search documents…'), 'roadmap')
    expect(await screen.findByRole('option', { name: /Doc q/ })).toBeVisible()
    expect(api.searchDocuments).toHaveBeenLastCalledWith({ q: 'roadmap', teamId: 'team-1' }, expect.anything())
    await userEvent.click(screen.getByRole('option', { name: /Doc q/ }))
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 'q' }))
  })

  it('finds a document by content and highlights safe text snippets', async () => {
    api.searchDocuments.mockImplementation(async ({ q }: { q?: string }) => ({ nodes: q ? [{ id: 'doc-content', title: 'Operations notes', teamIds: [], searchSnippet: 'The canary <script> deployment checklist.', matchedTerms: ['canary', '<script>'] }] : [], pageInfo: { hasNextPage: false } }))
    const onSelect = vi.fn()
    const { container } = render(<I18nProvider><LoopDocumentPicker data={makeBootstrap()} open onOpenChange={vi.fn()} onSelect={onSelect}/></I18nProvider>)
    await userEvent.type(screen.getByPlaceholderText('Search documents…'), 'canary')
    const item = await screen.findByRole('option', { name: /Operations notes/ })
    expect(item).toHaveTextContent('The canary <script> deployment checklist.')
    expect(item.querySelector('mark')).toHaveTextContent('canary')
    expect(item.querySelector('script')).toBeNull()
    expect(container.querySelector('script')).toBeNull()
    await userEvent.click(item)
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ id: 'doc-content' }))
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

  it('describe issue, comment and status update targets and declare title placeholders', () => {
    const data = makeBootstrap()
    const t = (source: string) => source
    const project = data.projects[0]
    expect(contractTargetText({ kind: 'comment', operation: 'create', required: true, target: { mode: 'triggering' } }, data, t)).toBe('The triggering item')
    expect(contractTargetText({ kind: 'comment', operation: 'create', required: false, target: { mode: 'matching', match: 'open bugs', projectId: project.id } }, data, t)).toBe(`issues matching open bugs · in ${project.name}`)
    expect(contractTargetText({ kind: 'statusUpdate', operation: 'create', required: true, target: { mode: 'existing', project: 'Mobile App' } }, data, t)).toBe('on Mobile App')
    expect(contractTargetText({ kind: 'issue', operation: 'create', required: false, target: { mode: 'new', titleTemplate: 'Bug: {topic}', placeholders: [{ name: 'topic', source: 'the subject' }] } }, data, t)).toBe('titled Bug: {topic} · {topic} filled by the run from the subject')
    const saved = personContract({ source: 'model', readOnly: false, outputs: [{ kind: 'issue', operation: 'create', required: true, target: { titleTemplate: 'Bug: {topic} {date} {area}', placeholders: [{ name: 'topic', source: 'x' }] } }] })
    expect(saved.outputs[0].target.placeholders).toEqual([{ name: 'topic', source: 'x' }, { name: 'area' }])
    expect(contractOutputSummary({ kind: 'document', operation: 'append', required: true, target: { mode: 'existing', title: '团队日报汇总' }, cardinality: 'onePerRun' }, data, t)).toBe('Append · Document · the document 团队日报汇总 · One per run')
  })

  it('has Chinese copy for every panel string', () => {
    for (const key of ['What this loop will produce', 'Outputs this run worked to', CONTRACT_FALLBACK_NOTICE, 'Must produce', 'Edit outputs', 'Save outputs', 'Use automatic outputs', 'Reply only: the run changes nothing', 'Title template', 'titled {title}', 'One per run', 'Create or update', 'Read only', 'Status update', 'Other change',
      'Target', 'Anywhere', 'The triggering item', 'Issues matching a description', 'A project', 'An initiative', 'Which issues', 'Project', 'Initiative', 'What fills {name}', 'Filled by the run from…', 'document not chosen yet', 'issues matching {match}', 'on {project}', 'in {project}', 'on {initiative}', '{name} filled by the run from {source}',
      'Choose document…', 'Search documents…', 'Recent documents', 'Create a new document', 'Create a new document “{title}”', 'Load more', 'All teams', 'Could not load documents', "The instructions don't say which document. Choose it, or create a new one.", "The instructions don't say which document to write to. Until you choose it, runs write to no existing document and need review.", 'Choose the document for each output that writes to an existing one.', "The loop's target document is not chosen", 'Instructions changed — review suggested outputs', 'Review suggested outputs', 'Keep my outputs', 'Keep mine', 'Accept suggestion', 'Your output', 'Suggested output', 'Save review', 'No output changes suggested', 'Compare each output with the suggested contract, then choose what to keep.', 'Accept all', 'What changed', 'Choose an existing document', 'Output {number}', 'New output', 'Removed output', 'Skip conditions', 'Conditions', 'Alternative group', 'Alternative outputs changed', 'Title placeholders', 'Document title', 'Document not chosen', 'Team name', 'Project name', 'Initiative name', 'Target description', 'Not set']) {
      expect(zhCN[key], key).toBeTruthy()
    }
  })
})
