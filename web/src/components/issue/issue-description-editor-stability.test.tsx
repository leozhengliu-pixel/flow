import { act, render, waitFor } from '@testing-library/react'
import type { Editor } from '@tiptap/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { IssueDescriptionEditor } from './issue-description-editor'
import { serializeDescription } from './editor/editor-content'
import { viewer } from '@/test/fixtures'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value }) }))
vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'stability' }))

const collab = { workspaceKey: 'w', documentId: 'd', viewer, onPersist: async () => {} }
const markdown = '# Title\n\nIntro\n\n```ts\nconst a: number = 1\nfunction f(b: string) { return b + "c" }\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |\n\n> quote\n\n- [x] done\n- [ ] todo\n\nTail\n'

async function mount(props: Partial<Parameters<typeof IssueDescriptionEditor>[0]>) {
  let editor: Editor | null = null
  render(<IssueDescriptionEditor value={markdown} outline editorRef={current => { editor = current }} collaboration={collab} {...props}/>)
  await waitFor(() => expect(editor).not.toBeNull())
  return editor as unknown as Editor
}

/** Counts editor transactions and updates while the page sits idle for `ms`. */
async function idleActivity(editor: Editor, ms = 1200) {
  let transactions = 0
  let updates = 0
  const onTransaction = () => { transactions++ }
  const onUpdate = () => { updates++ }
  editor.on('transaction', onTransaction)
  editor.on('update', onUpdate)
  await act(async () => { await new Promise(resolve => setTimeout(resolve, ms)) })
  editor.off('transaction', onTransaction)
  editor.off('update', onUpdate)
  return { transactions, updates }
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
  vi.stubGlobal('WebSocket', class { static OPEN = 1; readyState = 0; send() {} close() {} })
  Range.prototype.getClientRects = () => [] as unknown as DOMRectList
  Range.prototype.getBoundingClientRect = () => new DOMRect()
})

describe('editor stability with code blocks, tables and the outline', () => {
  it('opens a Markdown-seeded document without any transaction or update loop', async () => {
    const editor = await mount({})
    expect(editor.getJSON().content?.map(node => node.type)).toEqual(expect.arrayContaining(['heading', 'codeBlock', 'table', 'blockquote', 'taskList']))
    expect(await idleActivity(editor)).toEqual({ transactions: 0, updates: 0 })
  })

  it('opens a document stored as JSON without Yjs state (the shape history snapshots have) without a loop', async () => {
    const seed = await mount({})
    const snapshot = serializeDescription(seed)
    const editor = await mount({ value: 'x', state: snapshot.documentJSON, collaboration: { ...collab, documentId: 'json-only' } })
    expect(editor.getJSON().content?.map(node => node.type)).toContain('table')
    expect(await idleActivity(editor)).toEqual({ transactions: 0, updates: 0 })
  })

  it('reopens from the stored Yjs state and stays quiet', async () => {
    const seed = await mount({})
    const snapshot = serializeDescription(seed)
    const editor = await mount({ value: snapshot.markdown, state: snapshot.documentJSON, collaboration: { ...collab, documentId: 'reopened', contentState: snapshot.contentState, documentVersion: 1 } })
    expect(editor.getJSON().content?.map(node => node.type)).toContain('codeBlock')
    expect(await idleActivity(editor)).toEqual({ transactions: 0, updates: 0 })
  })

  it('answers each edit in a code block and a table cell with a bounded number of transactions', async () => {
    const editor = await mount({})
    const caretIn = (type: string) => {
      let pos = -1
      editor.state.doc.descendants((node, at) => {
        if (pos < 0 && node.type.name === type) pos = type === 'codeBlock' ? at + node.nodeSize - 1 : at + 2
      })
      return pos
    }
    for (const type of ['codeBlock', 'tableCell']) {
      act(() => { editor.commands.focus(caretIn(type)) })
      const before = { count: 0 }
      const count = () => { before.count++ }
      editor.on('transaction', count)
      act(() => { editor.commands.insertContent('x') })
      await act(async () => { await new Promise(resolve => setTimeout(resolve, 400)) })
      editor.off('transaction', count)
      expect(before.count, type).toBeLessThanOrEqual(4)
    }
  })

  it('highlights a code block once per edit, not once per render', async () => {
    const highlight = await import('@/components/editor/code-block/languages')
    const spy = vi.spyOn(highlight.hljs, 'highlight')
    const editor = await mount({})
    const initial = spy.mock.calls.length
    await idleActivity(editor, 600)
    expect(spy.mock.calls.length).toBe(initial)
    spy.mockRestore()
  })
})
