import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { afterEach, beforeAll, describe, expect, it } from 'vitest'
import { InlineCommentMark } from '@/components/issue/editor/inline-comment-mark'
import { flowTableKit } from './table'
import { ClipboardOrigin, clipboardOrigin, editorClipboardDocumentId, isFlowClipboardHtml, withClipboardMarker } from './clipboard-origin'

let current: Editor | null = null
afterEach(() => { current?.destroy(); current = null })
beforeAll(() => {
  Object.defineProperty(document, 'elementFromPoint', { configurable: true, value: () => null })
  Object.defineProperty(Range.prototype, 'getClientRects', { configurable: true, value: () => [] })
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', { configurable: true, value: () => new DOMRect(0, 0, 1, 1) })
})

function makeEditor(documentId: string | undefined, content: string) {
  const element = document.createElement('div')
  document.body.append(element)
  current = new Editor({ element, extensions: [StarterKit, InlineCommentMark, ...flowTableKit, ClipboardOrigin.configure({ getDocumentId: () => documentId })], content })
  return current
}

function clipboard(initial: Record<string, string> = {}) {
  const values = { ...initial }
  return {
    values,
    data: { get types() { return Object.keys(values) }, files: [], items: [], getData: (type: string) => values[type] ?? '', setData: (type: string, value: string) => { values[type] = value }, clearData: () => { for (const key of Object.keys(values)) delete values[key] } },
  }
}

function copy(editor: Editor, type: 'copy' | 'cut' = 'copy') {
  editor.commands.selectAll()
  const board = clipboard()
  const event = new Event(type, { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: board.data })
  editor.view.dom.dispatchEvent(event)
  return board.values
}

function paste(editor: Editor, html: string, text = 'x') {
  const board = clipboard({ 'text/html': html, 'text/plain': text })
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: board.data })
  editor.view.dom.dispatchEvent(event)
}

describe('clipboard marker helpers', () => {
  it('prepends an empty span carrying the document id, once', () => {
    const html = withClipboardMarker('<p>a</p>', 'doc-1')
    expect(html).toBe('<span data-flow-doc="doc-1"></span><p>a</p>')
    expect(withClipboardMarker(html, 'doc-2')).toBe(html)
    expect(withClipboardMarker('', 'doc-1')).toBe('')
  })

  it('reads the origin back, escaping quotes, and is null for foreign html', () => {
    expect(clipboardOrigin(withClipboardMarker('<p>a</p>', 'doc-1'))).toBe('doc-1')
    expect(clipboardOrigin(withClipboardMarker('<p>a</p>', 'a"b<c'))).toBe('a"b<c')
    expect(clipboardOrigin(withClipboardMarker('<p>a</p>'))).toBe('')
    expect(clipboardOrigin('<table><tr><td>a</td></tr></table>')).toBeNull()
    expect(isFlowClipboardHtml('<span data-doc-id="x"></span><p>a</p>')).toBe(false)
    expect(isFlowClipboardHtml(withClipboardMarker('<p>a</p>', 'd'))).toBe(true)
  })
})

describe('ClipboardOrigin', () => {
  it('marks copied and cut html with the document id and keeps ProseMirror slice data', () => {
    const editor = makeEditor('issue-1', '<p>hello</p>')
    for (const type of ['copy', 'cut'] as const) {
      const html = copy(editor, type)['text/html']
      expect(html.startsWith('<span data-flow-doc="issue-1"></span>')).toBe(true)
      expect(html).toContain('data-pm-slice')
    }
  })

  it('marks a copied table the same way', () => {
    const editor = makeEditor('issue-1', '<table><tr><th><p>A</p></th></tr><tr><td><p>b</p></td></tr></table><p></p>')
    const html = copy(editor)['text/html']
    expect(html.startsWith('<span data-flow-doc="issue-1"></span><table')).toBe(true)
  })

  it('exposes the editor document id to the table line copy', () => {
    expect(editorClipboardDocumentId(makeEditor('issue-7', '<p>a</p>'))).toBe('issue-7')
    current?.destroy()
    expect(editorClipboardDocumentId(makeEditor(undefined, '<p>a</p>'))).toBe('')
  })

  const anchored = '<p><span data-comment-id="c-1">anchored</span> text</p>'
  const hasAnchor = (editor: Editor) => JSON.stringify(editor.getJSON()).includes('inlineComment')

  it('keeps comment anchors when the clipboard came from the same document', () => {
    const editor = makeEditor('issue-1', '<p></p>')
    editor.commands.focus('end')
    paste(editor, withClipboardMarker(anchored, 'issue-1'))
    expect(hasAnchor(editor)).toBe(true)
  })

  it('drops comment anchors that point into another document', () => {
    const editor = makeEditor('issue-2', '<p></p>')
    editor.commands.focus('end')
    paste(editor, withClipboardMarker(anchored, 'issue-1'))
    expect(JSON.stringify(editor.getJSON())).toContain('anchored')
    expect(hasAnchor(editor)).toBe(false)
  })

  it('does not turn a Flow table without header flag into a header-row table again', () => {
    const editor = makeEditor('issue-2', '<p></p>')
    editor.commands.focus('end')
    paste(editor, withClipboardMarker('<table><tbody><tr><td><p>x</p></td></tr></tbody></table>', 'issue-1'))
    expect(JSON.stringify(editor.getJSON())).toContain('tableCell')
    expect(JSON.stringify(editor.getJSON())).not.toContain('tableHeader')
  })
})
