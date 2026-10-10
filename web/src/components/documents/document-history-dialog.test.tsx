import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { teammate, viewer } from '@/test/fixtures'
import type { DocumentRevision, FlowDocument } from '@/types/flow'

import { DocumentHistoryDialog } from './document-history-dialog'
import type { PMNode } from './document-history-diff'

async function readCss(name: string) {
  const fs = (await import(/* @vite-ignore */ `node:${'fs'}`)) as { readFileSync: (path: string, encoding: 'utf8') => string }
  return fs.readFileSync(`src/components/documents/${name}`, 'utf8')
}

const toastSuccess = vi.fn()
const toastError = vi.fn()
vi.mock('sonner', () => ({ toast: { success: (...args: unknown[]) => toastSuccess(...args), error: (...args: unknown[]) => toastError(...args) } }))

const text = (value: string, marks?: PMNode['marks']): PMNode => ({ type: 'text', text: value, ...(marks ? { marks } : {}) })
const p = (value: string): PMNode => ({ type: 'paragraph', content: [text(value)] })
const doc = (...content: PMNode[]): Record<string, unknown> => ({ type: 'doc', content }) as unknown as Record<string, unknown>
const ago = (hours: number) => new Date(Date.now() - hours * 3_600_000).toISOString()

const v1 = doc({ type: 'heading', attrs: { level: 2 }, content: [text('Overview')] }, p('The first version has a short paragraph.'), { type: 'bulletList', content: [{ type: 'listItem', content: [p('Alpha item')] }, { type: 'listItem', content: [p('Beta item')] }] })
const v2 = doc({ type: 'heading', attrs: { level: 2 }, content: [text('Overview')] }, p('The initial version has a short paragraph.'), { type: 'bulletList', content: [{ type: 'listItem', content: [p('Alpha item')] }, { type: 'listItem', content: [p('Beta item')] }] }, { type: 'taskList', content: [{ type: 'taskItem', attrs: { checked: true }, content: [p('Ship it')] }] })

function revision(id: string, title: string, contentData: Record<string, unknown> | undefined, createdAt: string, content = '', author = viewer): DocumentRevision {
  return { id, documentId: 'doc-1', title, content, contentData, author, createdAt }
}

function makeDocument(overrides: Partial<FlowDocument> = {}): FlowDocument {
  return {
    id: 'doc-1', slugId: 'doc-1', title: 'Roadmap', content: 'current', contentData: v2, creator: viewer, projectIds: [], teamIds: [], subscriberIds: [], favorite: false,
    createdAt: ago(80), updatedAt: ago(1), revisions: [
      revision('rev-2', 'Roadmap', v1, ago(26), 'prev one', viewer),
      revision('rev-1', 'Roadmap', undefined, ago(50), '## Heading from markdown\n\nLine one\nline two\n\n- first\n- second\n\n- [x] done\n- [ ] todo', teammate),
    ], ...overrides,
  }
}

function renderDialog(document: FlowDocument, props: Partial<{ onRestore: (id: string) => Promise<void>; onOpenChange: (open: boolean) => void }> = {}) {
  const onRestore = props.onRestore ?? vi.fn(async () => undefined)
  const onOpenChange = props.onOpenChange ?? vi.fn()
  const view = render(<I18nProvider><DocumentHistoryDialog document={document} onOpenChange={onOpenChange} onRestore={onRestore} open/></I18nProvider>)
  return { ...view, onRestore, onOpenChange }
}

const options = () => screen.getAllByRole('option')

beforeEach(() => {
  localStorage.setItem('flow:locale', 'en-US')
  toastSuccess.mockReset()
  toastError.mockReset()
})

describe('DocumentHistoryDialog', () => {
  // Versions are dated hours ago and grouped by day: pin the clock to midday so they don't cross midnight.
  beforeEach(() => { vi.useFakeTimers({ toFake: ['Date'] }); vi.setSystemTime(new Date(2026, 9, 9, 12)) })
  afterEach(() => { vi.useRealTimers() })

  it('titles the modal with the document and lists versions grouped by day with the Current tag and authors', () => {
    renderDialog(makeDocument())
    expect(screen.getByRole('dialog')).toHaveAccessibleName(/Restore version for\s*Roadmap/)
    expect(screen.getByRole('button', { name: 'Close modal dialog' })).toBeInTheDocument()
    const groups = screen.getAllByRole('group')
    expect(groups.map(group => group.getAttribute('aria-label'))).toEqual(['Today', 'Yesterday', expect.any(String)])
    expect(options()).toHaveLength(3)
    const [current] = options()
    expect(within(current).getByText('Current')).toBeInTheDocument()
    expect(screen.getAllByText('Current')).toHaveLength(1)
    expect(within(current).getByText('Viewer')).toBeInTheDocument()
    expect(within(options()[2]).getByText('Teammate')).toBeInTheDocument()
    expect(current.querySelector('.document-history-avatar')).toHaveAttribute('aria-label', 'Viewer')
    expect(current).toHaveAttribute('aria-selected', 'true')
  })

  it('renders the stored ProseMirror JSON as a document, never raw Markdown or literal newlines', async () => {
    const user = userEvent.setup()
    renderDialog(makeDocument())
    const view = screen.getByRole('document')
    expect(within(view).getByRole('heading', { level: 2, name: 'Overview' })).toBeInTheDocument()
    expect(within(view).getAllByRole('listitem').length).toBeGreaterThanOrEqual(2)
    expect(view.querySelector('[data-type="taskList"] [data-checked="true"]')).toBeTruthy()
    expect(view.textContent).not.toContain('\\n')
    expect(view.textContent).not.toContain('##')
    await user.click(options()[1])
    expect(within(screen.getByRole('document')).getByText(/first version/)).toBeInTheDocument()
  })

  it('falls back to the Markdown projection when a revision has no contentData', async () => {
    const user = userEvent.setup()
    renderDialog(makeDocument())
    await user.click(options()[2])
    const view = screen.getByRole('document')
    expect(within(view).getByRole('heading', { level: 2, name: 'Heading from markdown' })).toBeInTheDocument()
    expect(within(view).getAllByRole('listitem')).toHaveLength(4)
    expect(view.textContent).not.toContain('\\n')
    expect(view.textContent).not.toContain('##')
    expect(view.querySelectorAll('br')).toHaveLength(1) // "Line one" / "line two"
  })

  it('highlights what changed against the version before it and counts the changes', async () => {
    const user = userEvent.setup()
    renderDialog(makeDocument())
    // Current vs rev-2: "first" -> "initial" and the added checklist.
    expect(screen.getByTestId('history-change-counter')).toHaveTextContent('1 of 2')
    const view = screen.getByRole('document')
    expect(view.querySelector('del')).toHaveTextContent('first')
    expect(view.querySelector('ins')).toHaveTextContent('initial')
    expect(view.querySelector('[data-type="taskList"] > li.is-diff-added, [data-type="taskList"].is-diff-added')).toBeTruthy()
    // rev-2 vs rev-1 (markdown only): heading text differs, so there are changes too.
    await user.click(options()[1])
    expect(Number(/of (\d+)/.exec(screen.getByTestId('history-change-counter').textContent ?? '')?.[1])).toBeGreaterThan(0)
  })

  it('moves between changes with wrapping, scrolling the active one into view', async () => {
    const user = userEvent.setup()
    const scroll = vi.fn()
    Object.defineProperty(Element.prototype, 'scrollIntoView', { configurable: true, value: scroll })
    renderDialog(makeDocument())
    const counter = screen.getByTestId('history-change-counter')
    expect(counter).toHaveTextContent('1 of 2')
    await waitFor(() => expect(scroll).toHaveBeenCalled())
    expect(document.querySelector('[data-change-id="1"][data-active]')).toBeTruthy()
    scroll.mockClear()
    await user.click(screen.getByRole('button', { name: 'Next change' }))
    expect(counter).toHaveTextContent('2 of 2')
    expect(document.querySelector('[data-change-id="2"][data-active]')).toBeTruthy()
    expect(document.querySelector('[data-change-id="1"][data-active]')).toBeNull()
    expect(scroll).toHaveBeenCalledTimes(1)
    await user.click(screen.getByRole('button', { name: 'Next change' }))
    expect(counter).toHaveTextContent('1 of 2')
    await user.click(screen.getByRole('button', { name: 'Previous change' }))
    expect(counter).toHaveTextContent('2 of 2')
    Object.defineProperty(Element.prototype, 'scrollIntoView', { configurable: true, value: () => undefined })
  })

  it('removes all marks and the navigator when Highlight changes is off', async () => {
    const user = userEvent.setup()
    renderDialog(makeDocument())
    expect(screen.getByRole('document').querySelector('ins')).toBeTruthy()
    await user.click(screen.getByRole('checkbox', { name: 'Highlight changes' }))
    const view = screen.getByRole('document')
    expect(view.querySelector('ins, del, .is-diff-added, .is-diff-removed')).toBeNull()
    expect(screen.queryByTestId('history-change-counter')).toBeNull()
    expect(screen.queryByRole('button', { name: 'Next change' })).toBeNull()
    await user.click(screen.getByRole('checkbox', { name: 'Highlight changes' }))
    expect(screen.getByTestId('history-change-counter')).toHaveTextContent('1 of 2')
  })

  it('restores the selected revision, shows busy, then closes and toasts', async () => {
    const user = userEvent.setup()
    let finish: () => void = () => undefined
    const onRestore = vi.fn(() => new Promise<void>(resolve => { finish = resolve }))
    const { onOpenChange } = renderDialog(makeDocument(), { onRestore })
    const restore = screen.getByRole('button', { name: 'Restore version' })
    expect(restore).toBeDisabled()
    expect(restore).toHaveAttribute('title', 'This is the current version, you cannot restore it')
    await user.click(options()[1])
    expect(restore).toBeEnabled()
    await user.click(restore)
    expect(onRestore).toHaveBeenCalledWith('rev-2')
    expect(restore).toBeDisabled()
    expect(onOpenChange).not.toHaveBeenCalled()
    await act(async () => { finish() })
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(toastSuccess).toHaveBeenCalledWith('Content has been restored.', expect.anything())
  })

  it('keeps the dialog open and reports when the restore fails', async () => {
    const user = userEvent.setup()
    const onRestore = vi.fn(async () => { throw new Error('nope') })
    const { onOpenChange } = renderDialog(makeDocument(), { onRestore })
    await user.click(options()[1])
    await user.click(screen.getByRole('button', { name: 'Restore version' }))
    await waitFor(() => expect(toastError).toHaveBeenCalled())
    expect(onOpenChange).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Restore version' })).toBeEnabled()
  })

  it('moves the selection with the arrow keys', () => {
    renderDialog(makeDocument())
    const list = screen.getByRole('listbox', { name: 'Document versions' })
    list.focus()
    fireEvent.keyDown(list, { key: 'ArrowDown' })
    expect(options()[1]).toHaveAttribute('aria-selected', 'true')
    fireEvent.keyDown(list, { key: 'ArrowDown' })
    fireEvent.keyDown(list, { key: 'ArrowDown' })
    expect(options()[2]).toHaveAttribute('aria-selected', 'true')
    fireEvent.keyDown(list, { key: 'ArrowUp' })
    expect(options()[1]).toHaveAttribute('aria-selected', 'true')
    fireEvent.keyDown(list, { key: 'Home' })
    expect(options()[0]).toHaveAttribute('aria-selected', 'true')
    fireEvent.keyDown(list, { key: 'End' })
    expect(options()[2]).toHaveAttribute('aria-selected', 'true')
  })

  it('shows the empty state when there is no history', () => {
    renderDialog(makeDocument({ revisions: [] }))
    expect(screen.getByText('There is no history yet.')).toBeInTheDocument()
    expect(screen.queryByRole('listbox')).toBeNull()
  })

  it('closes with Escape', async () => {
    const user = userEvent.setup()
    const { onOpenChange } = renderDialog(makeDocument())
    await user.keyboard('{Escape}')
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('works when the server stores the pre-update state (revisions[0] is the previous state)', () => {
    renderDialog(makeDocument())
    expect(options()).toHaveLength(3)
    expect(within(options()[0]).getByText('Current')).toBeInTheDocument()
  })

  it('works when the server snapshots after each edit (revisions[0] equals the live document): no duplicate Current row', () => {
    const live = makeDocument()
    const snapshot = makeDocument({ revisions: [revision('rev-now', 'Roadmap', v2, ago(1), 'current'), ...makeDocument().revisions] })
    expect(snapshot.title).toBe(live.title)
    renderDialog(snapshot)
    expect(options()).toHaveLength(3)
    expect(screen.getAllByText('Current')).toHaveLength(1)
    // The author of the live version is the last editor (revisions[0]).
    expect(within(options()[0]).getByText('Viewer')).toBeInTheDocument()
  })

  it('shows the zh-CN copy', () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    renderDialog(makeDocument())
    expect(screen.getByText('恢复版本：')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '恢复版本' })).toBeInTheDocument()
    expect(screen.getAllByText('当前')).toHaveLength(1)
    expect(screen.getByRole('group', { name: '今天' })).toBeInTheDocument()
    expect(screen.getByText('高亮更改')).toBeInTheDocument()
  })
})

describe('history dialog css', () => {
  let css = ''
  let viewCss = ''
  beforeAll(async () => {
    css = await readCss('document-history-dialog.css')
    viewCss = await readCss('document-json-view.css')
  })

  it('fits the viewport with internal scroll areas', () => {
    expect(css).toMatch(/height:\s*min\(566px,\s*calc\(100dvh/)
    expect(css).toMatch(/max-height:\s*calc\(100dvh/)
    expect(css).toMatch(/\.document-history-scroll\s*{[^}]*overflow:\s*auto/)
    expect(css).toMatch(/\.document-history-list\s*{[^}]*overflow:\s*auto/)
    expect(css).toMatch(/\.document-history-pill\s*{[^}]*flex-wrap:\s*wrap/)
  })

  it('stacks the list under the preview on narrow screens and uses tokens only', () => {
    expect(css).toMatch(/@media \(max-width: 720px\)[\s\S]*grid-template-columns:\s*minmax\(0, 1fr\)/)
    expect(css).not.toMatch(/#[\da-f]{3,8}\b|rgba?\(|hsla?\(|lch\(/i)
    expect(viewCss).not.toMatch(/#[\da-f]{3,8}\b|rgba?\(|hsla?\(|lch\(/i)
  })
})
