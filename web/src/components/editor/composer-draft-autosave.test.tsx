import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Editor } from '@tiptap/react'
import { Composer } from './composer'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { ApiError } from '@/lib/api-client'
import { composerDraftKey } from '@/lib/composer-drafts'
import { viewer } from '@/test/fixtures'
import type { Draft } from '@/types/flow'

// A controllable draft API: every request waits until the test answers it, and the answers act on an in-memory server that
// behaves like POST/PATCH/DELETE /api/drafts (a POST that carries a clientKey the server already has returns that draft).
type DraftRecord = Draft & { clientKey?: string }
type Pending = { kind: 'create' | 'update' | 'delete'; id?: string; input?: Record<string, unknown>; settled: boolean; apply: () => unknown; resolve: (value: unknown) => void; reject: (reason: unknown) => void }
const net = vi.hoisted(() => {
  const state = { pending: [] as Pending[], records: new Map<string, DraftRecord>(), seq: 0, missing: (id: string): unknown => new Error(`missing ${id}`) }
  const enqueue = (kind: Pending['kind'], id: string | undefined, input: Record<string, unknown> | undefined, apply: () => unknown) => new Promise((resolve, reject) => {
    state.pending.push({ kind, id, input, settled: false, apply, resolve, reject })
  })
  const now = () => new Date(Date.UTC(2026, 10, 15, 0, 0, state.seq)).toISOString()
  const create = (input: Record<string, unknown>) => enqueue('create', undefined, input, () => {
    const key = typeof input.clientKey === 'string' ? input.clientKey : ''
    const existing = key ? [...state.records.values()].find(item => item.clientKey === key && item.type === input.type && item.resourceId === input.resourceId) : undefined
    if (existing) { Object.assign(existing, input, { updatedAt: now() }); return { ...existing } }
    state.seq++
    const record = { ...input, id: `draft_${state.seq}`, userId: 'viewer', createdAt: now(), updatedAt: now() } as DraftRecord
    state.records.set(record.id, record)
    return { ...record }
  })
  const update = (id: string, input: Record<string, unknown>) => enqueue('update', id, input, () => {
    const record = state.records.get(id)
    if (!record) throw state.missing(id)
    Object.assign(record, input, { updatedAt: now() })
    return { ...record }
  })
  const remove = (id: string) => enqueue('delete', id, undefined, () => {
    if (!state.records.delete(id)) throw state.missing(id)
    return undefined
  })
  return { state, create, update, remove }
})
vi.mock('@/lib/api', async importOriginal => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  createDraft: vi.fn(net.create),
  updateDraft: vi.fn(net.update),
  deleteDraft: vi.fn(net.remove),
}))

const notFound = () => new ApiError('not found', 404, 'not found')
const networkDown = () => new TypeError('Failed to fetch')
const outcomes: Record<string, () => unknown> = {
  timeout: () => new DOMException('The operation timed out.', 'TimeoutError'),
  '502': () => new ApiError('Request failed: 502', 502),
  '503': () => new ApiError('Request failed: 503', 503),
  'network error (unknown outcome)': networkDown,
}

const originalRects = Object.getOwnPropertyDescriptor(Range.prototype, 'getClientRects')
const originalRect = Object.getOwnPropertyDescriptor(Range.prototype, 'getBoundingClientRect')
const originalFromPoint = Document.prototype.elementFromPoint
beforeAll(() => {
  Object.defineProperty(Range.prototype, 'getClientRects', { configurable: true, value: () => [] })
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', { configurable: true, value: () => new DOMRect(0, 0, 1, 1) })
  Document.prototype.elementFromPoint = function (this: Document) { return this.body }
})
afterAll(() => {
  if (originalRects) Object.defineProperty(Range.prototype, 'getClientRects', originalRects)
  else Reflect.deleteProperty(Range.prototype, 'getClientRects')
  if (originalRect) Object.defineProperty(Range.prototype, 'getBoundingClientRect', originalRect)
  else Reflect.deleteProperty(Range.prototype, 'getBoundingClientRect')
  Document.prototype.elementFromPoint = originalFromPoint
})

let issueSeq = 0
let issueId = ''
beforeEach(() => {
  net.state.pending.length = 0
  net.state.records.clear()
  net.state.missing = () => notFound()
  localStorage.clear()
  // Each test edits its own issue, so nothing a previous test left behind can be mistaken for this one's draft.
  issueSeq++
  issueId = `issue-autosave-${issueSeq}`
})
afterEach(() => { vi.useRealTimers() })

type Props = Parameters<typeof Composer>[0]
const tree = (props: Props) => <I18nProvider><TooltipProvider><Composer {...props}/></TooltipProvider></I18nProvider>
// The issue page's composer: the same props detail-pane passes, metadata as a fresh inline object on every render.
const issueProps = (overrides: Partial<Props> = {}): Props => ({ draftType: 'comment', draftResourceId: issueId, draftTitle: 'Autosave issue', draftMetadata: { resourceType: 'issue' }, drafts: [], onSubmit: vi.fn().mockResolvedValue(undefined), ...overrides })
async function mount(props: Props) {
  const view = render(tree(props))
  await screen.findByRole('textbox', { name: 'Leave a comment…' })
  vi.useFakeTimers()
  return view
}
const editor = () => (document.querySelector('.ProseMirror') as HTMLElement & { editor: Editor }).editor
const typeText = (text: string) => act(() => { const current = editor(); current.commands.insertContentAt(current.state.doc.content.size - 1, text) })
const tick = (ms = 0) => act(() => vi.advanceTimersByTimeAsync(ms))
const unsettled = (kind?: Pending['kind']) => net.state.pending.filter(request => !request.settled && (!kind || request.kind === kind))
const requests = (kind: Pending['kind']) => net.state.pending.filter(request => request.kind === kind)
async function answer(request: Pending, how: 'ok' | 'lose-response' | { error: unknown } = 'ok') {
  request.settled = true
  if (how === 'ok') {
    let value: unknown
    try { value = request.apply() } catch (error) { request.reject(error); await tick(); return }
    request.resolve(value)
  } else if (how === 'lose-response') {
    try { request.apply() } catch { /* the server's answer is lost either way */ }
    request.reject(networkDown())
  } else request.reject(how.error)
  await tick()
}
/** Answers every request in arrival order (and any request those answers lead to) and lets every timer run. */
async function settle() {
  for (let round = 0; round < 60; round++) {
    const next = unsettled()[0]
    if (next) await answer(next)
    else await tick(60_000)
    if (!unsettled().length) { await tick(60_000); if (!unsettled().length) return }
  }
  throw new Error('draft requests did not settle')
}
const recordsFor = (resourceId: string) => [...net.state.records.values()].filter(record => record.resourceId === resourceId)
const seeded = (resourceId: string, body: string, bodyData?: Record<string, unknown>): Draft => {
  const record = { id: `draft_seed_${resourceId}`, userId: viewer.id, type: 'comment', resourceId, title: 'Autosave issue', body, metadata: { resourceType: 'issue', ...(bodyData ? { bodyData } : {}) }, createdAt: '2026-11-01T00:00:00.000Z', updatedAt: '2026-11-01T00:00:00.000Z' } as Draft
  net.state.records.set(record.id, { ...record })
  return record
}

describe('comment draft autosave', () => {
  it('1. a slow first create with continued edits ends with exactly one draft holding the latest content', async () => {
    await mount(issueProps())
    await typeText('@')
    await tick(350)
    expect(requests('create')).toHaveLength(1)
    // The first POST is still pending while the user keeps typing and the debounce fires again and again.
    for (const text of [' a', 'b', 'c']) { await typeText(text); await tick(350) }
    expect(requests('create')).toHaveLength(1)
    await settle()
    expect(requests('create')).toHaveLength(1)
    expect(recordsFor(issueId)).toHaveLength(1)
    expect(recordsFor(issueId)[0].body).toBe('@ abc')
  })

  it.each(Object.keys(outcomes))('2. a PATCH that fails with %s keeps the draft and creates no other', async name => {
    const draft = seeded(issueId, 'first words')
    await mount(issueProps({ drafts: [draft] }))
    await typeText(' and more')
    await tick(350)
    expect(unsettled('update')).toHaveLength(1)
    await answer(unsettled('update')[0], { error: outcomes[name]() })
    await settle()
    expect(requests('create')).toHaveLength(0)
    expect(requests('update').every(request => request.id === draft.id)).toBe(true)
    expect(recordsFor(issueId)).toHaveLength(1)
    expect(recordsFor(issueId)[0].body).toBe('first words and more')
  })

  it('3. an update the server applied but whose response was lost is retried as the same draft, never copied', async () => {
    const draft = seeded(issueId, 'kept')
    await mount(issueProps({ drafts: [draft] }))
    await typeText(' safe')
    await tick(350)
    await answer(unsettled('update')[0], 'lose-response')
    await settle()
    expect(requests('create')).toHaveLength(0)
    expect(recordsFor(issueId).map(record => [record.id, record.body])).toEqual([[draft.id, 'kept safe']])
  })

  it('3. a create the server applied but whose response was lost is retried without a second draft', async () => {
    await mount(issueProps())
    await typeText('lost reply')
    await tick(350)
    await answer(unsettled('create')[0], 'lose-response')
    await typeText(' and later')
    await tick(350)
    await settle()
    expect(recordsFor(issueId)).toHaveLength(1)
    expect(recordsFor(issueId)[0].body).toBe('lost reply and later')
  })

  it('4. switching issues with responses arriving out of order keeps each draft on its own issue', async () => {
    const first = issueId, second = `${issueId}-other`
    const view = await mount(issueProps())
    await typeText('for the first issue')
    await tick(350)
    view.rerender(tree(issueProps({ draftResourceId: second })))
    await tick()
    const switchedText = editor().getText()
    await typeText('for the second issue')
    await tick(350)
    const [firstCreate, secondCreate] = unsettled('create')
    expect(secondCreate?.input?.resourceId).toBe(second)
    await answer(secondCreate)
    await answer(firstCreate)
    await typeText(' again')
    await tick(350)
    await settle()
    expect(recordsFor(first).map(record => record.body)).toEqual(['for the first issue'])
    expect(recordsFor(second).map(record => record.body)).toEqual(['for the second issue again'])
    expect(requests('update').every(request => recordsFor(second).some(record => record.id === request.id) && request.input?.resourceId === second)).toBe(true)
    expect(switchedText).toBe('')
  })

  it('4. unmounting while the first create is pending and coming back makes no second draft', async () => {
    const view = await mount(issueProps())
    await typeText('typed before leaving')
    await tick(350)
    expect(unsettled('create')).toHaveLength(1)
    view.unmount()
    vi.useRealTimers()
    // data.drafts has not caught up yet: the page comes back with no draft for the issue.
    await mount(issueProps())
    const restoredText = editor().getText()
    await typeText(', back again')
    await tick(350)
    await settle()
    expect(requests('create')).toHaveLength(1)
    expect(recordsFor(issueId)).toHaveLength(1)
    expect(restoredText).toBe('typed before leaving')
    expect(recordsFor(issueId)[0].body).toBe('typed before leaving, back again')
  })

  it('5. sending while the first create is in flight leaves no draft behind', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined)
    await mount(issueProps({ onSubmit }))
    await typeText('send me')
    await tick(350)
    expect(unsettled('create')).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: 'Submit comment' }))
    await tick()
    expect(onSubmit).toHaveBeenCalledWith('send me', expect.anything())
    await settle()
    expect(recordsFor(issueId)).toHaveLength(0)
    expect(localStorage.getItem(composerDraftKey('comment', issueId))).toBeNull()
    expect(editor().getText()).toBe('')
  })

  it('5. sending while an update is in flight does not bring the draft back after the delete', async () => {
    const draft = seeded(issueId, 'almost')
    await mount(issueProps({ drafts: [draft] }))
    await typeText(' sent')
    await tick(350)
    const patch = unsettled('update')[0]
    expect(patch).toBeDefined()
    fireEvent.click(screen.getByRole('button', { name: 'Submit comment' }))
    await tick()
    // If the client deletes right away, the delete reaches the server before the PATCH does.
    for (const remove of unsettled('delete')) await answer(remove)
    await answer(patch)
    await settle()
    expect(recordsFor(issueId)).toHaveLength(0)
    expect(localStorage.getItem(composerDraftKey('comment', issueId))).toBeNull()
    expect(editor().getText()).toBe('')
  })

  it('6. a failed send restores the body, its mention and its attachment, and keeps the one draft', async () => {
    const bodyData = { type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'see ' }, { type: 'mention', attrs: { id: 'app-restore', label: 'Build agent' } }, { type: 'text', text: ' here' }] }, { type: 'image', attrs: { src: 'https://files.example.test/shot.png', alt: 'shot' } }] }
    const draft = seeded(issueId, 'see @Build agent here', bodyData)
    const onSubmit = vi.fn().mockRejectedValueOnce(new Error('Network down')).mockResolvedValue(undefined)
    const { container } = await mount(issueProps({ drafts: [draft], onSubmit }))
    fireEvent.click(screen.getByRole('button', { name: 'Submit comment' }))
    await tick()
    await settle()
    expect(screen.getByRole('alert')).toHaveTextContent('Network down')
    expect(editor().getText()).toContain('see')
    expect(JSON.stringify(editor().getJSON())).toContain('app-restore')
    expect(container.querySelector('img[src="https://files.example.test/shot.png"]')).not.toBeNull()
    expect(recordsFor(issueId).map(record => record.id)).toEqual([draft.id])
    expect(requests('create')).toHaveLength(0)
    fireEvent.click(screen.getByRole('button', { name: 'Submit comment' }))
    await tick()
    await settle()
    expect(onSubmit).toHaveBeenCalledTimes(2)
    expect(JSON.stringify(onSubmit.mock.calls[1][1])).toContain('app-restore')
    expect(JSON.stringify(onSubmit.mock.calls[1][1])).toContain('https://files.example.test/shot.png')
    expect(recordsFor(issueId)).toHaveLength(0)
  })

  it('7. an unchanged draft sends nothing however often the parent re-renders', async () => {
    const draft = seeded(issueId, 'already saved')
    const view = await mount(issueProps({ drafts: [draft] }))
    for (let render = 0; render < 6; render++) {
      view.rerender(tree(issueProps({ drafts: [draft], draftMetadata: { resourceType: 'issue' } })))
      await tick(200)
    }
    await tick(5_000)
    expect(net.state.pending).toHaveLength(0)
  })

  it('7. one edit followed by parent re-renders sends one save', async () => {
    const view = await mount(issueProps())
    await typeText('only once')
    for (let render = 0; render < 6; render++) {
      view.rerender(tree(issueProps({ draftMetadata: { resourceType: 'issue' } })))
      await tick(100)
    }
    await settle()
    for (let render = 0; render < 6; render++) {
      view.rerender(tree(issueProps({ draftMetadata: { resourceType: 'issue' } })))
      await tick(400)
    }
    expect(net.state.pending.map(request => request.kind)).toEqual(['create'])
    expect(recordsFor(issueId).map(record => record.body)).toEqual(['only once'])
  })
})
