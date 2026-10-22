import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { registerAppNavigator } from '@/lib/app-navigation'
import { IssueDescriptionEditor } from './issue-description-editor'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value }) }))
vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'editor-test' }))

// A browser does not focus the anchor inside editable text on mousedown; user-event does, so dispatch the events directly.
function press(target: Element) {
  fireEvent.mouseDown(target)
  fireEvent.mouseUp(target)
  fireEvent.click(target, { detail: 1 })
}

describe('description editor links', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
    Document.prototype.elementFromPoint = function elementFromPoint(this: Document) { return this.body }
  })
  afterEach(() => vi.unstubAllGlobals())

  it('opens external links in a new tab and workspace links in-app; a click while editing only selects the link', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const navigate = vi.fn()
    const unregister = registerAppNavigator(navigate)
    const user = userEvent.setup()
    try {
      render(<><IssueDescriptionEditor value={'See [outside](https://example.com/a), [inside](/acme/issue/FLO-1/title) and more'}/><button type="button">elsewhere</button></>)
      const outside = await screen.findByRole('link', { name: 'outside' })
      const inside = screen.getByRole('link', { name: 'inside' })
      const body = outside.closest<HTMLElement>('.ProseMirror')!
      // jsdom does not focus a bare contenteditable; a tabindex lets focus() work like it does in a browser.
      body.setAttribute('tabindex', '0')
      press(outside)
      expect(open).toHaveBeenCalledWith('https://example.com/a', '_blank', 'noopener,noreferrer')
      body.focus()
      press(inside)
      expect(navigate).not.toHaveBeenCalled()
      expect(window.getSelection()?.toString()).toBe('inside')
      await user.click(screen.getByRole('button', { name: 'elsewhere' }))
      press(inside)
      expect(navigate).toHaveBeenCalledWith('/acme/issue/FLO-1/title')
    } finally {
      unregister()
      open.mockRestore()
    }
  })
})
