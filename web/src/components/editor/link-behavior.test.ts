import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { registerAppNavigator } from '@/lib/app-navigation'
import { LinkBehavior, openableLinkUrl } from './link-behavior'

const originalRects = Object.getOwnPropertyDescriptor(Range.prototype, 'getClientRects')
const originalRect = Object.getOwnPropertyDescriptor(Range.prototype, 'getBoundingClientRect')
const originalFromPoint = Document.prototype.elementFromPoint
beforeAll(() => {
  Object.defineProperty(Range.prototype, 'getClientRects', { configurable: true, value: () => [] })
  Object.defineProperty(Range.prototype, 'getBoundingClientRect', { configurable: true, value: () => new DOMRect(0, 0, 1, 1) })
  Document.prototype.elementFromPoint = function elementFromPoint(this: Document) { return this.body }
})
afterAll(() => {
  if (originalRects) Object.defineProperty(Range.prototype, 'getClientRects', originalRects)
  else Reflect.deleteProperty(Range.prototype, 'getClientRects')
  if (originalRect) Object.defineProperty(Range.prototype, 'getBoundingClientRect', originalRect)
  else Reflect.deleteProperty(Range.prototype, 'getBoundingClientRect')
  Document.prototype.elementFromPoint = originalFromPoint
})

const html = '<p>Intro <a href="https://example.com/page">external</a> and <a href="/acme/issue/FLO-1/title">issue</a> end</p>'
let editor: Editor
let openSpy: ReturnType<typeof vi.spyOn>
let navigate: ReturnType<typeof vi.fn<(path: string) => void>>
let unregister: () => void

function mount(editable = true, content = html) {
  const element = document.createElement('div')
  document.body.append(element)
  editor = new Editor({ element, editable, content, extensions: [StarterKit.configure({ link: { openOnClick: false, autolink: true } }), LinkBehavior] })
  return editor
}
const anchor = (text: string) => [...editor.view.dom.querySelectorAll('a')].find(item => item.textContent === text) as HTMLAnchorElement
function press(target: Element, init: MouseEventInit = {}) {
  const down = new MouseEvent('mousedown', { bubbles: true, cancelable: true, button: 0, ...init })
  target.dispatchEvent(down)
  const click = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0, detail: 1, ...init })
  target.dispatchEvent(click)
  return click
}
const focused = (value: boolean) => vi.spyOn(editor.view, 'hasFocus').mockReturnValue(value)

beforeEach(() => {
  openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
  navigate = vi.fn<(path: string) => void>()
  unregister = registerAppNavigator(navigate)
})
afterEach(() => {
  editor.destroy()
  unregister()
  vi.restoreAllMocks()
  window.getSelection()?.removeAllRanges()
  document.body.replaceChildren()
})

describe('link behaviour in editors', () => {
  it('opens an external link in a new tab, without opener, when the text is not being edited', () => {
    mount()
    focused(false)
    const click = press(anchor('external'))
    expect(click.defaultPrevented).toBe(true)
    expect(openSpy).toHaveBeenCalledWith('https://example.com/page', '_blank', 'noopener,noreferrer')
    expect(navigate).not.toHaveBeenCalled()
  })

  it('navigates a workspace URL through the router instead of reloading', () => {
    mount()
    focused(false)
    press(anchor('issue'))
    expect(navigate).toHaveBeenCalledWith('/acme/issue/FLO-1/title')
    expect(openSpy).not.toHaveBeenCalled()
  })

  it('navigates an absolute same-origin workspace URL in-app too', () => {
    mount(true, `<p><a href="${window.location.origin}/acme/projects/all">projects</a></p>`)
    focused(false)
    press(anchor('projects'))
    expect(navigate).toHaveBeenCalledWith('/acme/projects/all')
  })

  it('opens a same-origin path that is not an app route (an API download) in a new tab', () => {
    mount(true, '<p><a href="/api/files/report.pdf">file</a></p>')
    focused(false)
    press(anchor('file'))
    expect(navigate).not.toHaveBeenCalled()
    expect(openSpy).toHaveBeenCalledWith(`${window.location.origin}/api/files/report.pdf`, '_blank', 'noopener,noreferrer')
  })

  it('opens in a new tab with Cmd/Ctrl-click, even a workspace URL', () => {
    mount()
    focused(false)
    press(anchor('issue'), { metaKey: true })
    press(anchor('external'), { ctrlKey: true })
    expect(navigate).not.toHaveBeenCalled()
    expect(openSpy).toHaveBeenCalledTimes(2)
    expect(openSpy).toHaveBeenNthCalledWith(1, `${window.location.origin}/acme/issue/FLO-1/title`, '_blank', 'noopener,noreferrer')
  })

  it('does not hijack editing: with the caret in the editor a click selects the whole link and opens nothing', () => {
    mount()
    focused(true)
    const click = press(anchor('external'))
    expect(click.defaultPrevented).toBe(true)
    expect(openSpy).not.toHaveBeenCalled()
    expect(navigate).not.toHaveBeenCalled()
    const { from, to } = editor.state.selection
    expect(editor.state.doc.textBetween(from, to)).toBe('external')
  })

  it('does not open a link while text is selected or on a double click', () => {
    mount()
    focused(false)
    const selection = window.getSelection()
    selection?.selectAllChildren(anchor('external'))
    press(anchor('external'))
    selection?.removeAllRanges()
    const double = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0, detail: 2 })
    anchor('external').dispatchEvent(double)
    expect(openSpy).not.toHaveBeenCalled()
  })

  it('opens links in read-only text even when the page has focus', () => {
    mount(false)
    focused(true)
    press(anchor('external'))
    press(anchor('issue'))
    expect(openSpy).toHaveBeenCalledTimes(1)
    expect(navigate).toHaveBeenCalledWith('/acme/issue/FLO-1/title')
  })

  it('opens an editor link in a new tab on a middle click', () => {
    mount()
    const aux = new MouseEvent('auxclick', { bubbles: true, cancelable: true, button: 1 })
    anchor('issue').dispatchEvent(aux)
    expect(aux.defaultPrevented).toBe(true)
    expect(openSpy).toHaveBeenCalledWith(`${window.location.origin}/acme/issue/FLO-1/title`, '_blank', 'noopener,noreferrer')
  })

  it('never opens a javascript: or data: URL', () => {
    mount()
    focused(false)
    const link = anchor('external')
    for (const href of ['javascript:alert(1)', ' JaVaScRiPt:alert(1)', 'data:text/html,<b>x</b>', 'vbscript:x']) {
      link.setAttribute('href', href)
      const click = press(link)
      expect(click.defaultPrevented).toBe(true)
      press(link, { metaKey: true })
    }
    expect(openSpy).not.toHaveBeenCalled()
    expect(navigate).not.toHaveBeenCalled()
  })

  it('shows the link host as its hover hint', () => {
    mount(true, '<p><a href="https://www.example.org/x">site</a> <a href="mailto:dev@example.com">mail</a></p>')
    expect(anchor('site').querySelector('[data-tooltip-text]')?.getAttribute('data-tooltip-text')).toBe('www.example.org')
    expect(anchor('mail').querySelector('[data-tooltip-text]')?.getAttribute('data-tooltip-text')).toBe('dev@example.com')
  })
})

describe('openableLinkUrl', () => {
  it('allows only http, https and mailto', () => {
    expect(openableLinkUrl('https://a.test/x')?.href).toBe('https://a.test/x')
    expect(openableLinkUrl('mailto:a@b.test')?.protocol).toBe('mailto:')
    expect(openableLinkUrl('/acme/issue/FLO-1')?.origin).toBe(window.location.origin)
    for (const value of ['javascript:alert(1)', 'tel:123', 'file:///etc/passwd', 'blob:x', '', undefined, null]) expect(openableLinkUrl(value)).toBeUndefined()
  })
})
