import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { act } from 'react'
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { Comment } from '@/types/flow'

const api = vi.hoisted(() => ({
  createEntityComment: vi.fn(),
  updateEntityComment: vi.fn(),
  deleteEntityComment: vi.fn(),
  resolveEntityComment: vi.fn(),
  toggleEntityCommentReaction: vi.fn(),
  setEntityThreadSubscription: vi.fn(),
}))
const toasts = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }))
vi.mock('@/lib/toast', () => ({ toast: toasts }))
vi.mock('@/lib/api', async importOriginal => ({ ...(await importOriginal<typeof import('@/lib/api')>()), ...api }))
// The draft and reply composers are full editors; a textarea keeps these tests on the thread behaviour.
vi.mock('@/components/editor/composer', () => ({
  Composer: ({ placeholder, onSubmit }: { placeholder: string; onSubmit: (body: string) => Promise<void> }) => {
    let value = ''
    return <form aria-label={placeholder} onSubmit={event => { event.preventDefault(); void onSubmit(value) }}>
      <textarea aria-label={placeholder} onChange={event => { value = event.target.value }}/>
      <button type="submit">Send</button>
    </form>
  },
}))
vi.mock('@/components/activity/rich-comment', () => ({ RichComment: ({ body }: { body: string }) => <p>{body}</p> }))

import { partitionAnchoredComments } from '@/components/documents/inline-comments/inline-comments-model'
import { EntityInlineComments, EntityResolvedCommentsPanel, type InlineCommentEntity } from './entity-inline-comments'
import { useEntityInlineComments } from './entity-inline-comments-state'

beforeAll(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})

const entity: InlineCommentEntity = { kind: 'project', id: 'project-1', name: 'Launch', href: '/acme/project/launch-1', teamId: 'team-1' }

function comment(overrides: Partial<Comment>): Comment {
  return { id: 'comment-1', version: 1, body: 'Is this final?', reactions: {}, createdAt: '2026-10-01T10:00:00.000Z', user: teammate, ...overrides }
}

let editors: Editor[] = []
function makeEditor() {
  const element = document.createElement('div')
  document.body.appendChild(element)
  const editor = new Editor({ element, extensions: [StarterKit], content: '<p>Alpha bravo charlie delta echo.</p>' })
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
function setViewport(width: number) {
  window.matchMedia = ((query: string) => ({ matches: width >= Number(/min-width: (\d+)px/.exec(query)?.[1] ?? 0), media: query, addEventListener: () => {}, removeEventListener: () => {}, addListener: () => {}, removeListener: () => {}, onchange: null, dispatchEvent: () => false })) as typeof window.matchMedia
}

/** A description host like the project overview: the hook's editor and shell refs, the gutter and the resolved panel. */
function Host({ comments, editor, onReload, resolvedOpen = false, railOpen = false, onState }: { comments: Comment[]; editor: Editor; onReload: () => Promise<void>; resolvedOpen?: boolean; railOpen?: boolean; onState?: (state: ReturnType<typeof useEntityInlineComments>) => void }) {
  const state = useEntityInlineComments(comments)
  const { attachShell: shellRef } = state
  onState?.(state)
  const data = makeBootstrap()
  return <div ref={shellRef}>
    <button type="button" onClick={() => state.attachEditor(editor as never)}>attach</button>
    <EntityInlineComments state={state} entity={entity} data={data} railOpen={railOpen} onReload={onReload}/>
    {resolvedOpen && <EntityResolvedCommentsPanel state={state} entity={entity} data={data} onReload={onReload} onClose={vi.fn()}/>}
  </div>
}

beforeEach(() => { setViewport(1470); Object.values(api).forEach(mock => mock.mockReset()); toasts.success.mockReset(); toasts.error.mockReset() })
afterEach(() => { editors.forEach(editor => editor.destroy()); editors = []; document.body.innerHTML = '' })

describe('inline threads on a project or initiative description', () => {
  it('splits an entity\'s comments into inline threads (with their replies) and page-level discussion', () => {
    const { anchored, pageLevel } = partitionAnchoredComments([
      comment({ id: 'inline', anchorId: 'cmt_a', quotedText: 'charlie' }),
      comment({ id: 'inline-reply', parentId: 'inline' }),
      comment({ id: 'page' }),
      comment({ id: 'page-reply', parentId: 'page' }),
    ])
    expect(anchored.map(item => item.id)).toEqual(['inline', 'inline-reply'])
    expect(pageLevel.map(item => item.id)).toEqual(['page', 'page-reply'])
  })

  it('underlines the quoted text of open inline threads and shows only those threads in the gutter', () => {
    const editor = makeEditor()
    const comments = [comment({ id: 'inline', anchorId: 'cmt_a', quotedText: 'charlie delta', body: 'Inline question' }), comment({ id: 'page', body: 'Page-level comment' })]
    render(<I18nProvider><Host comments={comments} editor={editor} onReload={vi.fn()}/></I18nProvider>)
    fireEvent.click(screen.getByRole('button', { name: 'attach' }))
    // Descriptions are markdown: the thread finds its text by quote and never writes a mark into it.
    expect(editor.view.dom.querySelector('.inline-comment-open')?.textContent).toBe('charlie delta')
    expect(editor.getHTML()).not.toContain('data-comment-id')
    const gutter = screen.getByRole('complementary', { name: 'Comments' })
    expect(within(gutter).getByText('Inline question')).toBeInTheDocument()
    expect(within(gutter).queryByText('Page-level comment')).not.toBeInTheDocument()
  })

  it('saves a new thread on the entity with an anchor and the quote, then reloads', async () => {
    const editor = makeEditor()
    const onReload = vi.fn().mockResolvedValue(undefined)
    api.createEntityComment.mockResolvedValue(comment({ id: 'created', anchorId: 'cmt_new', quotedText: 'bravo' }))
    let state: ReturnType<typeof useEntityInlineComments> | undefined
    render(<I18nProvider><Host comments={[]} editor={editor} onReload={onReload} onState={next => { state = next }}/></I18nProvider>)
    fireEvent.click(screen.getByRole('button', { name: 'attach' }))
    act(() => state!.startComment({ ...rangeOf(editor, 'bravo'), text: 'bravo' }))
    expect(editor.view.dom.querySelector('.inline-comment-pending')?.textContent).toBe('bravo')
    fireEvent.change(screen.getByRole('textbox', { name: 'Add a comment…' }), { target: { value: 'Why bravo?' } })
    fireEvent.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(onReload).toHaveBeenCalled())
    expect(api.createEntityComment).toHaveBeenCalledWith({ kind: 'project', id: 'project-1' }, expect.objectContaining({ body: 'Why bravo?', quotedText: 'bravo', anchorId: expect.stringMatching(/^cmt_/) }))
  })

  it('resolves a thread through the entity API and lists it under Resolved comments with the page it is on', async () => {
    const editor = makeEditor()
    const onReload = vi.fn().mockResolvedValue(undefined)
    api.resolveEntityComment.mockResolvedValue(undefined)
    const open = [comment({ id: 'inline', anchorId: 'cmt_a', quotedText: 'charlie', body: 'Inline question' })]
    const view = render(<I18nProvider><Host comments={open} editor={editor} onReload={onReload}/></I18nProvider>)
    fireEvent.click(screen.getByRole('button', { name: 'attach' }))
    fireEvent.click(screen.getByRole('button', { name: 'Resolve thread' }))
    await waitFor(() => expect(api.resolveEntityComment).toHaveBeenCalledWith({ kind: 'project', id: 'project-1' }, 'inline', true, 1))
    view.rerender(<I18nProvider><Host comments={[{ ...open[0], resolved: true }]} editor={editor} onReload={onReload} resolvedOpen/></I18nProvider>)
    expect(editor.view.dom.querySelector('.inline-comment-open')).toBeNull()
    const panel = screen.getByRole('complementary', { name: 'Resolved comments' })
    expect(within(panel).getByText('Launch')).toBeInTheDocument()
    expect(within(panel).getByText('Inline question')).toBeInTheDocument()
  })

  it('opens the thread as a popover while a side panel takes the room beside the text', () => {
    const editor = makeEditor()
    const comments = [comment({ id: 'inline', anchorId: 'cmt_a', quotedText: 'charlie', body: 'Inline question', user: viewer })]
    let state: ReturnType<typeof useEntityInlineComments> | undefined
    render(<I18nProvider><Host comments={comments} editor={editor} onReload={vi.fn()} railOpen onState={next => { state = next }}/></I18nProvider>)
    fireEvent.click(screen.getByRole('button', { name: 'attach' }))
    // Popover rules: no gutter cards (the column does not move) until the thread's text is clicked.
    expect(screen.queryByText('Inline question')).not.toBeInTheDocument()
    expect(state!.gutter).toBe(false)
    fireEvent.click(editor.view.dom.querySelector('.inline-comment-open')!)
    expect(screen.getByRole('complementary', { name: 'Comments' })).toHaveClass('is-popover')
    expect(screen.getByText('Inline question')).toBeInTheDocument()
  })
})
