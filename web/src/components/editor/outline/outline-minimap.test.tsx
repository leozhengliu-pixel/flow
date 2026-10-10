import { act, fireEvent, render, screen } from '@testing-library/react'
import { Editor, type JSONContent } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { createRef } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { OutlineMinimap } from './outline-minimap'
import { collectHeadings } from './outline-headings'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value }) }))

const doc = (...blocks: JSONContent[]): JSONContent => ({ type: 'doc', content: blocks })
const heading = (level: number, text: string): JSONContent => ({ type: 'heading', attrs: { level }, content: [{ type: 'text', text }] })
const paragraph = (text: string): JSONContent => ({ type: 'paragraph', content: [{ type: 'text', text }] })

const editors: Editor[] = []
function makeEditor(content: JSONContent) {
  const element = document.createElement('div')
  document.body.appendChild(element)
  const editor = new Editor({ element, extensions: [StarterKit], content })
  editors.push(editor)
  return editor
}

describe('collectHeadings', () => {
  afterEach(() => { editors.splice(0).forEach(editor => editor.destroy()) })

  it('returns H1-H4 (not H5) with level, text, position and unique slugs', () => {
    const editor = makeEditor(doc(heading(1, 'Scope'), paragraph('x'), heading(2, 'Scope'), heading(4, 'Deep'), heading(5, 'Deeper'), heading(3, 'Plan B')))
    const list = collectHeadings(editor)
    expect(list.map(item => [item.level, item.text, item.slug])).toEqual([[1, 'Scope', 'scope'], [2, 'Scope', 'scope-1'], [4, 'Deep', 'deep'], [3, 'Plan B', 'plan-b']])
    expect(editor.state.doc.nodeAt(list[0].pos)?.type.name).toBe('heading')
  })

  it('is empty for no editor or no headings', () => {
    expect(collectHeadings(null)).toEqual([])
    expect(collectHeadings(makeEditor(doc(paragraph('only text'))))).toEqual([])
  })
})

describe('OutlineMinimap', () => {
  beforeEach(() => { vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { cb(0); return 1 }) })
  afterEach(() => { editors.splice(0).forEach(editor => editor.destroy()); vi.unstubAllGlobals(); vi.restoreAllMocks() })

  const renderOutline = (editor: Editor) => render(<OutlineMinimap editor={editor} rootRef={createRef<HTMLElement>()}/>)

  it('renders nothing without headings', () => {
    const { container } = renderOutline(makeEditor(doc(paragraph('text'))))
    expect(container.querySelector('.doc-outline')).toBeNull()
  })

  it('renders a row per heading with level classes', () => {
    renderOutline(makeEditor(doc(heading(1, 'Intro'), heading(2, 'Details'), heading(3, 'Fine print'))))
    expect(screen.getByRole('navigation', { name: 'Document outline' })).toBeTruthy()
    const rows = screen.getAllByRole('button')
    expect(rows).toHaveLength(3)
    expect(rows[0].className).toContain('doc-outline__row--l1')
    expect(rows[1].className).toContain('doc-outline__row--l2')
    expect(rows[2].className).toContain('doc-outline__row--l3')
    expect(rows[1].textContent).toBe('Details')
  })

  it('marks the heading at the top of the viewport as active', () => {
    const editor = makeEditor(doc(heading(1, 'One'), heading(1, 'Two'), heading(1, 'Three')))
    const list = collectHeadings(editor)
    const tops = [-400, 60, 600]
    list.forEach((item, index) => {
      const dom = editor.view.nodeDOM(item.pos) as HTMLElement
      dom.getBoundingClientRect = () => new DOMRect(0, tops[index], 100, 20)
    })
    class FakeIntersectionObserver { observe() {} unobserve() {} disconnect() {} }
    vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver)
    renderOutline(editor)
    const active = screen.getAllByRole('button').filter(row => row.classList.contains('is-active'))
    expect(active.map(row => row.textContent)).toEqual(['Two'])
    expect(active[0].getAttribute('aria-current')).toBe('location')
  })

  it('anchors to the scroll area left edge, centred in its visible rect', () => {
    const scroller = document.createElement('div')
    scroller.style.overflowY = 'auto'
    scroller.getBoundingClientRect = () => new DOMRect(244, 40, 1226, 666)
    const root = document.createElement('div')
    scroller.appendChild(root)
    document.body.appendChild(scroller)
    const ref = { current: root }
    const { container } = render(<OutlineMinimap editor={makeEditor(doc(heading(1, 'One')))} rootRef={ref}/>)
    const nav = container.querySelector('.doc-outline') as HTMLElement
    expect(nav.style.getPropertyValue('--outline-left')).toBe('250px')
    expect(nav.style.getPropertyValue('--outline-top')).toBe('373px')
    scroller.remove()
  })

  it('smooth-scrolls to the heading when a row is clicked', () => {
    const editor = makeEditor(doc(heading(1, 'One'), heading(2, 'Two')))
    const scroll = vi.fn()
    Object.defineProperty(Element.prototype, 'scrollIntoView', { configurable: true, writable: true, value: scroll })
    renderOutline(editor)
    fireEvent.click(screen.getByRole('button', { name: 'Two' }))
    expect(scroll).toHaveBeenCalledWith({ behavior: 'smooth', block: 'start' })
    expect(scroll.mock.contexts[0]).toBe(editor.view.nodeDOM(collectHeadings(editor)[1].pos))
  })

  it('updates when headings change (a burst of updates is collapsed into one refresh)', async () => {
    const editor = makeEditor(doc(heading(1, 'One')))
    const { container } = renderOutline(editor)
    expect(screen.getAllByRole('button')).toHaveLength(1)
    await act(async () => { editor.commands.setContent(doc(heading(1, 'One'), heading(2, 'Added'))); await Promise.resolve() })
    expect(screen.getAllByRole('button').map(row => row.textContent)).toEqual(['One', 'Added'])
    await act(async () => { editor.commands.setContent(doc(paragraph('gone'))); await Promise.resolve() })
    expect(container.querySelector('.doc-outline')).toBeNull()
  })
})
