import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { Composer } from './composer'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { registerAppNavigator } from '@/lib/app-navigation'

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

// A browser does not focus the anchor inside editable text on mousedown; user-event does, so dispatch the events directly.
function press(target: Element) {
  fireEvent.mouseDown(target)
  fireEvent.mouseUp(target)
  fireEvent.click(target, { detail: 1 })
}

describe('comment composer links', () => {
  it('selects the link while editing and opens it once the editor is left', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const navigate = vi.fn()
    const unregister = registerAppNavigator(navigate)
    const user = userEvent.setup()
    try {
      render(<I18nProvider><TooltipProvider><Composer onSubmit={vi.fn()}/><button type="button">elsewhere</button></TooltipProvider></I18nProvider>)
      const box = await screen.findByRole('textbox', { name: 'Leave a comment…' })
      // jsdom does not focus a bare contenteditable; a tabindex lets the click focus it like a browser does.
      box.setAttribute('tabindex', '0')
      await user.click(box)
      await user.type(box, 'see https://example.com/x then ')
      const link = await screen.findByRole('link', { name: 'https://example.com/x' })
      press(link)
      expect(open).not.toHaveBeenCalled()
      expect(window.getSelection()?.toString()).toBe('https://example.com/x')
      await user.click(screen.getByRole('button', { name: 'elsewhere' }))
      press(link)
      expect(open).toHaveBeenCalledWith('https://example.com/x', '_blank', 'noopener,noreferrer')
      expect(navigate).not.toHaveBeenCalled()
    } finally {
      unregister()
      open.mockRestore()
    }
  })

  // The selection toolbar shows a selection after a short delay. A delay still
  // pending when the composer goes away must not bring the toolbar back: it
  // used to reattach to the page (or, after a test file's environment was torn
  // down, throw "document is not defined").
  it('does not show the selection toolbar after the composer unmounts', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const user = userEvent.setup()
    try {
      const { unmount } = render(<I18nProvider><TooltipProvider><Composer onSubmit={vi.fn()}/></TooltipProvider></I18nProvider>)
      const box = await screen.findByRole('textbox', { name: 'Leave a comment…' })
      box.setAttribute('tabindex', '0')
      await user.click(box)
      await user.type(box, 'see https://example.com/x then ')
      press(await screen.findByRole('link', { name: 'https://example.com/x' }))
      expect(window.getSelection()?.toString()).toBe('https://example.com/x')
      const attached = Array.from(document.body.children)
      unmount()
      await new Promise(resolve => setTimeout(resolve, 400))
      expect(Array.from(document.body.children).filter(element => !attached.includes(element))).toEqual([])
    } finally {
      open.mockRestore()
    }
  })
})
