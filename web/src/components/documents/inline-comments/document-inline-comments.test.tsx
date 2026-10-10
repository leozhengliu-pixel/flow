import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

import { InlineCommentMark } from '@/components/issue/editor/inline-comment-mark'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, Comment, FlowDocument } from '@/types/flow'

const api = vi.hoisted(() => ({
  createDocumentComment: vi.fn(),
  updateDocumentComment: vi.fn(),
  deleteDocumentComment: vi.fn(),
  resolveDocumentComment: vi.fn(),
  toggleDocumentCommentReaction: vi.fn(),
  setDocumentThreadSubscription: vi.fn(),
}))
const toasts = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('sonner', () => ({ toast: toasts }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
// The reply/draft composer is a full editor; a textarea keeps these tests on the thread behaviour.
vi.mock('@/components/editor/composer', () => ({
  Composer: ({ placeholder, onSubmit, onCancel, initialValue = '' }: { placeholder: string; onSubmit: (body: string) => Promise<void>; onCancel?: () => void; initialValue?: string }) => {
    let value = initialValue
    return <form aria-label={placeholder} onSubmit={event => { event.preventDefault(); void onSubmit(value) }}>
      <textarea aria-label={placeholder} contentEditable defaultValue={initialValue} onChange={event => { value = event.target.value }}/>
      <button type="submit">Send</button>
      {onCancel && <button onClick={onCancel} type="button">Cancel</button>}
    </form>
  },
}))
vi.mock('@/components/activity/rich-comment', () => ({ RichComment: ({ body }: { body: string }) => <p>{body}</p> }))

import { buildThreads, findMarkRanges, findQuoteRange, layoutGutter } from './inline-comments-model'
import { createInlineCommentsPlugin, inlineCommentsPluginKey } from './inline-comments-plugin'
import { DocumentInlineComments, DocumentResolvedCommentsButton, ResolvedCommentsPanel, type CommentDraft } from './document-inline-comments'

beforeAll(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})

const flowDocument = {
  id: 'document-1', slugId: 'plan-1a2b3c4d5e6f', title: 'Plan', content: '', creator: viewer,
  projectIds: [], teamIds: ['team-1'], subscriberIds: [viewer.id], favorite: false,
  createdAt: '2026-09-01T02:30:00.000Z', updatedAt: '2026-09-01T04:49:00.000Z', revisions: [],
} as FlowDocument

function comment(overrides: Partial<Comment>): Comment {
  return { id: 'comment-1', version: 1, body: 'Is this final?', reactions: {}, createdAt: '2026-10-01T10:00:00.000Z', user: teammate, ...overrides }
}

let editors: Editor[] = []
function makeEditor(html = '<p>The launch plan covers three phases.</p><p>Phase two adds comments.</p>') {
  const element = document.createElement('div')
  document.body.appendChild(element)
  const editor = new Editor({ element, extensions: [StarterKit, InlineCommentMark], content: html })
  editors.push(editor)
  return editor
}
function rangeOf(editor: Editor, text: string) {
  let found: { from: number; to: number } | undefined
  editor.state.doc.descendants((node, pos) => {
    if (found || !node.isText) return
    const index = node.text!.indexOf(text)
    if (index >= 0) found = { from: pos + index, to: pos + index + text.length }
  })
  return found!
}

/** The viewport decides between gutter cards (1232px and wider) and a popover, through a media query. */
function setViewport(width: number) {
  window.matchMedia = ((query: string) => ({ matches: width >= Number(/min-width: (\d+)px/.exec(query)?.[1] ?? 0), media: query, addEventListener: () => {}, removeEventListener: () => {}, addListener: () => {}, removeListener: () => {}, onchange: null, dispatchEvent: () => false })) as typeof window.matchMedia
}

function renderComments(props: { data?: BootstrapData; comments: Comment[]; editor: Editor | null; draft?: CommentDraft; onDraftChange?: (draft?: CommentDraft) => void; canEdit?: boolean; canComment?: boolean; viewport?: number; railOpen?: boolean }) {
  const data = props.data ?? makeBootstrap({ documents: [flowDocument] })
  const shell = document.createElement('div')
  if (props.viewport !== undefined) setViewport(props.viewport)
  const onReload = vi.fn().mockResolvedValue(undefined)
  const view = render(<I18nProvider><DocumentInlineComments data={data} document={flowDocument} comments={props.comments} editor={props.editor} shell={shell} draft={props.draft} onDraftChange={props.onDraftChange ?? vi.fn()} canComment={props.canComment ?? true} canEdit={props.canEdit ?? true} onReload={onReload} railOpen={props.railOpen}/></I18nProvider>)
  return { ...view, onReload, data }
}

beforeEach(() => { setViewport(1470); Object.values(api).forEach(mock => mock.mockReset()); toasts.success.mockReset(); toasts.error.mockReset() })
afterEach(() => { editors.forEach(editor => editor.destroy()); editors = []; document.body.innerHTML = '' })

describe('inline comments model', () => {
  it('builds threads with replies (replies to replies join their root) and keeps legacy page comments', () => {
    const threads = buildThreads([
      comment({ id: 'root', anchorId: 'cmt_a', quotedText: 'two' }),
      comment({ id: 'reply', parentId: 'root', createdAt: '2026-10-01T11:00:00.000Z' }),
      comment({ id: 'nested', parentId: 'reply', createdAt: '2026-10-01T12:00:00.000Z' }),
      comment({ id: 'legacy', createdAt: '2026-09-30T10:00:00.000Z' }),
    ])
    expect(threads.map(thread => [thread.root.id, thread.anchorId, thread.replies.map(reply => reply.id)])).toEqual([
      ['legacy', '', []],
      ['root', 'cmt_a', ['reply', 'nested']],
    ])
  })

  it('finds mark ranges and quoted text nearest a hint', () => {
    const editor = makeEditor('<p>alpha beta</p><p>beta gamma</p>')
    const second = rangeOf(editor, 'beta gamma')
    editor.chain().setTextSelection({ from: second.from, to: second.from + 4 }).setMark('inlineComment', { commentId: 'cmt_x' }).run()
    expect(findMarkRanges(editor.state.doc).get('cmt_x')).toEqual({ from: second.from, to: second.from + 4 })
    expect(findQuoteRange(editor.state.doc, 'beta', second.from)).toEqual({ from: second.from, to: second.from + 4 })
    expect(findQuoteRange(editor.state.doc, 'missing')).toBeUndefined()
  })

  it('stacks gutter cards without overlap and keeps the active card at its anchor', () => {
    const items = [{ id: 'a', top: 0, height: 100 }, { id: 'b', top: 20, height: 50 }, { id: 'c', top: 300, height: 40 }]
    expect([...layoutGutter(items)]).toEqual([['a', 0], ['b', 108], ['c', 300]])
    expect(layoutGutter(items, 'b').get('b')).toBe(20)
    expect(layoutGutter(items, 'b').get('a')).toBe(20 - 8 - 100)
  })
})

describe('inline comments plugin', () => {
  it('underlines open threads, highlights the active one and the pending selection, and draws nothing for resolved threads', () => {
    const editor = makeEditor()
    editor.registerPlugin(createInlineCommentsPlugin())
    const range = rangeOf(editor, 'two adds')
    editor.chain().setTextSelection(range).setMark('inlineComment', { commentId: 'cmt_open' }).run()
    editor.view.dispatch(editor.state.tr.setMeta(inlineCommentsPluginKey, { open: [{ id: 'cmt_open' }, { id: 'cmt_quote', quote: 'launch plan' }] }))
    expect(editor.view.dom.querySelectorAll('.inline-comment-open')).toHaveLength(2)
    expect(inlineCommentsPluginKey.getState(editor.state)?.unmarked.has('cmt_quote')).toBe(true)
    editor.view.dispatch(editor.state.tr.setMeta(inlineCommentsPluginKey, { active: 'cmt_open' }))
    expect(editor.view.dom.querySelector('.inline-comment-open.is-active')?.textContent).toBe('two adds')
    const pending = rangeOf(editor, 'three')
    editor.view.dispatch(editor.state.tr.setMeta(inlineCommentsPluginKey, { pending }))
    // The pending range follows edits made before the comment is saved.
    editor.commands.insertContentAt(1, 'X')
    expect(inlineCommentsPluginKey.getState(editor.state)?.pending).toEqual({ from: pending.from + 1, to: pending.to + 1 })
    editor.view.dispatch(editor.state.tr.setMeta(inlineCommentsPluginKey, { open: [], active: null, pending: null }))
    expect(editor.view.dom.querySelectorAll('.inline-comment-open, .inline-comment-pending')).toHaveLength(0)
  })
})

describe('DocumentInlineComments', () => {
  const threads = [comment({ id: 'root', anchorId: 'cmt_a', quotedText: 'two adds' }), comment({ id: 'other', anchorId: 'cmt_b', quotedText: 'launch plan', body: 'Other thread' })]
  function markedEditor() {
    const editor = makeEditor()
    editor.chain().setTextSelection(rangeOf(editor, 'two adds')).setMark('inlineComment', { commentId: 'cmt_a' }).run()
    editor.chain().setTextSelection(rangeOf(editor, 'launch plan')).setMark('inlineComment', { commentId: 'cmt_b' }).run()
    return editor
  }

  it('lists every open thread in the gutter on a wide viewport and reports it so the document shifts left', async () => {
    const editor = markedEditor()
    const onGutterChange = vi.fn()
    const data = makeBootstrap({ documents: [flowDocument] })
    setViewport(1470)
    const shell = document.createElement('div')
    render(<I18nProvider><DocumentInlineComments data={data} document={flowDocument} comments={threads} editor={editor} shell={shell} onDraftChange={vi.fn()} canComment canEdit onReload={vi.fn()} onGutterChange={onGutterChange}/></I18nProvider>)
    expect(await screen.findAllByRole('group', { name: 'Comment thread' })).toHaveLength(2)
    expect(document.querySelector('.document-inline-gutter')).not.toHaveClass('is-popover')
    await waitFor(() => expect(onGutterChange).toHaveBeenLastCalledWith(true))
  })

  it('opens only the clicked thread as a 360px popover under its text on a narrow viewport, and leaves the document where it is', async () => {
    const editor = markedEditor()
    const onGutterChange = vi.fn()
    const data = makeBootstrap({ documents: [flowDocument] })
    setViewport(1200)
    const shell = document.createElement('div')
    render(<I18nProvider><DocumentInlineComments data={data} document={flowDocument} comments={threads} editor={editor} shell={shell} onDraftChange={vi.fn()} canComment canEdit onReload={vi.fn()} onGutterChange={onGutterChange}/></I18nProvider>)
    // Nothing is open until a highlight is clicked.
    await waitFor(() => expect(document.querySelector('.inline-comment-open')).toBeTruthy())
    expect(screen.queryAllByRole('group', { name: 'Comment thread' })).toHaveLength(0)
    fireEvent.click(editor.view.dom.querySelector<HTMLElement>('[data-comment-anchor="cmt_a"]')!)
    const cards = await screen.findAllByRole('group', { name: 'Comment thread' })
    expect(cards).toHaveLength(1)
    expect(cards[0].dataset.threadId).toBe('root')
    expect(document.querySelector('.document-inline-gutter')).toHaveClass('is-popover')
    expect(cards[0].style.left).not.toBe('')
    expect(cards[0].style.top).not.toBe('')
    expect(onGutterChange).not.toHaveBeenCalledWith(true)
    // Clicking outside the thread closes it.
    fireEvent.mouseDown(document.body)
    await waitFor(() => expect(screen.queryAllByRole('group', { name: 'Comment thread' })).toHaveLength(0))
  })

  it('uses the popover whenever the agent rail is open', async () => {
    const editor = markedEditor()
    renderComments({ comments: threads, editor, viewport: 1470, railOpen: true })
    fireEvent.click(await waitFor(() => editor.view.dom.querySelector<HTMLElement>('[data-comment-anchor="cmt_a"]')!))
    await screen.findByRole('group', { name: 'Comment thread' })
    expect(document.querySelector('.document-inline-gutter')).toHaveClass('is-popover')
  })

  it('shows a draft composer as a popover too, with the avatar and a one-row composer', async () => {
    const editor = makeEditor()
    const range = rangeOf(editor, 'launch plan')
    renderComments({ comments: [], editor, draft: { ...range, text: 'launch plan' }, viewport: 900 })
    expect(await screen.findByRole('textbox', { name: 'Add a comment…' })).toBeTruthy()
    expect(document.querySelector('.document-inline-gutter.is-popover .document-thread-card.is-draft')).toBeTruthy()
  })

  it('creates an anchored thread from the selection and writes the anchor mark', async () => {
    const editor = makeEditor()
    const range = rangeOf(editor, 'two adds comments')
    api.createDocumentComment.mockResolvedValue(comment({ id: 'new-thread', anchorId: 'x', user: viewer }))
    const onDraftChange = vi.fn()
    renderComments({ comments: [], editor, draft: { ...range, text: 'two adds comments' }, onDraftChange })
    const composer = await screen.findByRole('textbox', { name: 'Add a comment…' })
    await userEvent.type(composer, 'Separate phase?')
    await userEvent.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(api.createDocumentComment).toHaveBeenCalled())
    const [documentId, input] = api.createDocumentComment.mock.calls[0]
    expect(documentId).toBe('document-1')
    expect(input).toMatchObject({ body: 'Separate phase?', quotedText: 'two adds comments' })
    expect(input.anchorId).toMatch(/^cmt_[0-9a-f]+/)
    await waitFor(() => expect(findMarkRanges(editor.state.doc).get(input.anchorId)).toEqual(range))
    expect(onDraftChange).toHaveBeenCalledWith(undefined)
  })

  it('lets a commenter comment without editing the document (anchored by quote)', async () => {
    const editor = makeEditor()
    const range = rangeOf(editor, 'launch plan')
    api.createDocumentComment.mockResolvedValue(comment({ id: 'new-thread', user: viewer }))
    renderComments({ comments: [], editor, draft: { ...range, text: 'launch plan' }, canEdit: false })
    await userEvent.type(await screen.findByRole('textbox', { name: 'Add a comment…' }), 'Question')
    await userEvent.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(api.createDocumentComment).toHaveBeenCalled())
    expect(findMarkRanges(editor.state.doc).size).toBe(0)
  })

  it('shows open threads in the gutter, legacy page comments too, and activates a thread from its highlight', async () => {
    const editor = makeEditor()
    const range = rangeOf(editor, 'two adds')
    editor.chain().setTextSelection(range).setMark('inlineComment', { commentId: 'cmt_a' }).run()
    renderComments({ comments: [comment({ id: 'root', anchorId: 'cmt_a', quotedText: 'two adds' }), comment({ id: 'legacy', body: 'Page comment' }), comment({ id: 'done', anchorId: 'cmt_b', resolved: true, body: 'Resolved one' })], editor })
    const cards = await screen.findAllByRole('group', { name: 'Comment thread' })
    expect(cards).toHaveLength(2)
    expect(screen.queryByText('Resolved one')).toBeNull()
    expect(screen.getByText('Page comment')).toBeTruthy()
    const highlight = editor.view.dom.querySelector<HTMLElement>('[data-comment-anchor="cmt_a"]')!
    expect(highlight.classList.contains('inline-comment-open')).toBe(true)
    fireEvent.click(highlight)
    await waitFor(() => expect(editor.view.dom.querySelector('.inline-comment-open.is-active')).toBeTruthy())
    expect(cards.find(card => card.dataset.threadId === 'root')?.classList.contains('is-active')).toBe(true)
  })

  it('resolves, reacts and replies through the thread actions', async () => {
    const editor = makeEditor()
    api.resolveDocumentComment.mockResolvedValue(comment({ resolved: true }))
    api.toggleDocumentCommentReaction.mockResolvedValue(comment({}))
    api.createDocumentComment.mockResolvedValue(comment({ id: 'reply' }))
    const root = comment({ id: 'root', anchorId: 'cmt_a', quotedText: 'two adds', reactions: { '👍': [teammate.id] } })
    const { onReload } = renderComments({ comments: [root], editor })
    const card = await screen.findByRole('group', { name: 'Comment thread' })
    await userEvent.click(within(card).getByRole('button', { name: 'Resolve thread' }))
    await waitFor(() => expect(api.resolveDocumentComment).toHaveBeenCalledWith('document-1', 'root', true, 1))
    expect(onReload).toHaveBeenCalled()
    await userEvent.click(within(card).getByRole('button', { name: /1/ }))
    await waitFor(() => expect(api.toggleDocumentCommentReaction).toHaveBeenCalledWith('document-1', 'root', '👍'))
    await userEvent.click(within(card).getByRole('button', { name: 'Reply…' }))
    await userEvent.type(within(card).getByRole('textbox', { name: 'Reply…' }), 'Agreed')
    await userEvent.click(within(card).getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(api.createDocumentComment).toHaveBeenCalledWith('document-1', { body: 'Agreed', bodyData: undefined, parentId: 'root' }))
  })

  it('offers Edit and Delete only to the author or a workspace admin', async () => {
    const editor = makeEditor()
    const member = makeBootstrap({ documents: [flowDocument], viewerRole: 'member' })
    const mine = comment({ id: 'mine', anchorId: 'cmt_m', user: viewer, body: 'Mine' })
    const theirs = comment({ id: 'theirs', anchorId: 'cmt_t', user: teammate, body: 'Theirs' })
    const view = renderComments({ data: member, comments: [mine, theirs], editor })
    const menuItems = async (body: string) => {
      const card = (await screen.findByText(body)).closest<HTMLElement>('[role="group"]')!
      await userEvent.click(within(card).getByRole('button', { name: 'Comment options' }))
      const items = (await screen.findAllByRole('menuitem')).map(item => item.textContent)
      await userEvent.keyboard('{Escape}')
      return items
    }
    expect((await menuItems('Mine')).join('|')).toMatch(/Edit.*Delete/)
    const theirsItems = (await menuItems('Theirs')).join('|')
    expect(theirsItems).not.toMatch(/Edit|Delete/)
    expect(theirsItems).toMatch(/Unsubscribe from thread/)
    expect(theirsItems).toMatch(/Copy link to comment/)
    expect(theirsItems).toMatch(/New issue from comment/)
    view.unmount()
    renderComments({ data: makeBootstrap({ documents: [flowDocument], viewerRole: 'admin' }), comments: [theirs], editor })
    expect((await menuItems('Theirs')).join('|')).toMatch(/Edit.*Delete/)
  })

  it('deletes a thread and removes its anchor mark', async () => {
    const editor = makeEditor()
    const range = rangeOf(editor, 'two adds')
    editor.chain().setTextSelection(range).setMark('inlineComment', { commentId: 'cmt_a' }).run()
    api.deleteDocumentComment.mockResolvedValue(undefined)
    renderComments({ comments: [comment({ id: 'root', anchorId: 'cmt_a', user: viewer, body: 'Mine' })], editor })
    const card = (await screen.findByText('Mine')).closest<HTMLElement>('[role="group"]')!
    await userEvent.click(within(card).getByRole('button', { name: 'Comment options' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: /Delete/ }))
    await waitFor(() => expect(api.deleteDocumentComment).toHaveBeenCalledWith('document-1', 'root'))
    await waitFor(() => expect(findMarkRanges(editor.state.doc).size).toBe(0))
  })
})

describe('resolved comments', () => {
  it('shows the header button only when resolved threads exist', () => {
    const { rerender } = render(<I18nProvider><DocumentResolvedCommentsButton count={0} open={false} onToggle={vi.fn()}/></I18nProvider>)
    expect(screen.queryByRole('button', { name: 'Resolved comments' })).toBeNull()
    rerender(<I18nProvider><DocumentResolvedCommentsButton count={2} open={false} onToggle={vi.fn()}/></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Resolved comments' })).toBeTruthy()
  })

  it('lists resolved threads with their quote and reopens them', async () => {
    api.resolveDocumentComment.mockResolvedValue(comment({ resolved: false }))
    const onReload = vi.fn().mockResolvedValue(undefined)
    const resolved = comment({ id: 'done', anchorId: 'cmt_d', quotedText: 'three phases', resolved: true, body: 'Fixed it', reactions: { '🎉': [viewer.id] } })
    render(<I18nProvider><ResolvedCommentsPanel data={makeBootstrap({ documents: [flowDocument] })} document={flowDocument} comments={[resolved]} editor={null} canComment canEdit onReload={onReload} onClose={vi.fn()}/></I18nProvider>)
    const panel = screen.getByRole('complementary', { name: 'Resolved comments' })
    expect(within(panel).getByText('three phases')).toBeTruthy()
    expect(within(panel).getByText('Fixed it')).toBeTruthy()
    expect(within(panel).getByRole('button', { name: /🎉/ })).toBeTruthy()
    await userEvent.click(within(panel).getByRole('button', { name: 'Comment options' }))
    await userEvent.click(await screen.findByRole('menuitem', { name: /Reopen thread/ }))
    await waitFor(() => expect(api.resolveDocumentComment).toHaveBeenCalledWith('document-1', 'done', false, 1))
  })
})
