import { Editor, getSchema, type AnyExtension } from '@tiptap/core'
import { Collaboration } from '@tiptap/extension-collaboration'
import { StarterKit } from '@tiptap/starter-kit'
import { prosemirrorJSONToYDoc } from '@tiptap/y-tiptap'
import * as Y from 'yjs'
import { Markdown } from '@tiptap/markdown'
import { afterEach, describe, expect, it } from 'vitest'
import { FlowStarterKit } from './flow-starter-kit'

const editors: Editor[] = []
afterEach(() => { while (editors.length) editors.pop()!.destroy() })

function create(content: string, extensions: AnyExtension[] = [FlowStarterKit.configure({ undoRedo: false, link: { openOnClick: false } }), Markdown]) {
  const editor = new Editor({ element: document.createElement('div'), content, extensions })
  editors.push(editor)
  return editor
}

/** Marks nested inside each other, outermost first, for the text node "x". */
function nesting(html: string) {
  const editor = create(`<p>${html}</p>`)
  const element = document.createElement('div')
  element.innerHTML = editor.getHTML()
  const tags: string[] = []
  let node: Element | null = element.querySelector('p')
  while (node && node.firstElementChild) { node = node.firstElementChild; tags.push(node.tagName.toLowerCase()) }
  return tags
}

describe('mark nesting order', () => {
  it('orders the schema marks link, code, italic, strike, underline, bold', () => {
    const names = Object.keys(create('<p></p>').schema.marks)
    const order = ['link', 'code', 'italic', 'strike', 'underline', 'bold']
    expect(names.filter(name => order.includes(name))).toEqual(order)
  })

  it('writes bold italic text as <em><strong>', () => {
    expect(nesting('<strong><em>x</em></strong>')).toEqual(['em', 'strong'])
    expect(nesting('<em><strong>x</strong></em>')).toEqual(['em', 'strong'])
  })

  it('nests every pair the same way whichever way the source nests them', () => {
    const pairs: Array<[string, string]> = [['em', 'strong'], ['em', 'u'], ['em', 's'], ['s', 'u'], ['s', 'strong'], ['u', 'strong']]
    for (const [outer, inner] of pairs) {
      expect(nesting(`<${outer}><${inner}>x</${inner}></${outer}>`)).toEqual([outer, inner])
      expect(nesting(`<${inner}><${outer}>x</${outer}></${inner}>`)).toEqual([outer, inner])
    }
  })

  it('puts a link outside every other mark and code outside the text styles', () => {
    expect(nesting('<strong><a href="https://example.com">x</a></strong>')).toEqual(['a', 'strong'])
    expect(nesting('<a href="https://example.com"><em><strong>x</strong></em></a>')).toEqual(['a', 'em', 'strong'])
  })

  it('serialises bold italic markdown and reads it back', () => {
    const editor = create('<p><strong><em>x</em></strong></p>')
    const markdown = editor.getMarkdown().trim()
    expect(markdown).toBe('***x***')
    const again = new Editor({ element: document.createElement('div'), content: markdown, contentType: 'markdown', extensions: [FlowStarterKit.configure({ undoRedo: false }), Markdown] })
    editors.push(again)
    expect(again.getHTML()).toBe('<p><em><strong>x</strong></em></p>')
  })

  it('loads text stored with marks in either order and keeps editing it', () => {
    const editor = create({ type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'old', marks: [{ type: 'bold' }, { type: 'italic' }] }] }] } as unknown as string)
    expect(editor.getHTML()).toBe('<p><em><strong>old</strong></em></p>')
    editor.commands.setTextSelection({ from: 1, to: 4 })
    editor.commands.toggleUnderline()
    editor.commands.insertContentAt(4, ' new')
    expect(editor.getHTML()).toBe('<p><em><u><strong>old new</strong></u></em></p>')
  })

  it('opens a stored Yjs document written with the previous mark order and edits it', () => {
    const json = { type: 'doc', content: [{ type: 'paragraph', content: [{ type: 'text', text: 'stored', marks: [{ type: 'bold' }, { type: 'italic' }] }, { type: 'text', text: ' plain' }] }] }
    const stored = prosemirrorJSONToYDoc(getSchema([StarterKit.configure({ undoRedo: false })]), json, 'default')
    const update = Y.encodeStateAsUpdate(stored)
    const ydoc = new Y.Doc()
    Y.applyUpdate(ydoc, update)
    const editor = create('', [FlowStarterKit.configure({ undoRedo: false }), Collaboration.configure({ document: ydoc })])
    expect(editor.getHTML()).toBe('<p><em><strong>stored</strong></em> plain</p>')
    editor.commands.setTextSelection({ from: 8, to: 13 })
    editor.commands.toggleBold()
    expect(editor.getHTML()).toBe('<p><em><strong>stored</strong></em> <strong>plain</strong></p>')
    const reopened = new Y.Doc()
    Y.applyUpdate(reopened, Y.encodeStateAsUpdate(ydoc))
    const second = create('', [FlowStarterKit.configure({ undoRedo: false }), Collaboration.configure({ document: reopened })])
    expect(second.getHTML()).toBe(editor.getHTML())
  })
})
