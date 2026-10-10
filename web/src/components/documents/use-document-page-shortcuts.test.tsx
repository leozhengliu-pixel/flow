import { fireEvent, render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { requestDocumentUiAction } from './document-actions'
import { DOCUMENT_PAGE_SHORTCUTS, useDocumentPageShortcuts, useDocumentUiActions, type DocumentPageShortcutHandlers } from './use-document-page-shortcuts'

type Spies = DocumentPageShortcutHandlers & { [K in keyof DocumentPageShortcutHandlers]: ReturnType<typeof vi.fn> & (() => void) }
function handlers(): Spies {
  return Object.fromEntries(Object.keys(DOCUMENT_PAGE_SHORTCUTS).map(name => [name, vi.fn()])) as unknown as Spies
}

function Harness({ handlers: value, enabled = true }: { handlers: DocumentPageShortcutHandlers; enabled?: boolean }) {
  useDocumentPageShortcuts(value, enabled)
  return <div><input aria-label="field"/></div>
}

const press = (init: KeyboardEventInit & { code: string; key: string }, target: Element = document.body) => fireEvent.keyDown(target, init)

describe('useDocumentPageShortcuts', () => {
  vi.spyOn(navigator, 'platform', 'get').mockReturnValue('MacIntel')

  it('maps Linear key hints to the page handlers', () => {
    const fns = handlers()
    render(<Harness handlers={fns}/>)
    press({ code: 'KeyP', key: 'P', shiftKey: true })
    press({ code: 'KeyR', key: 'R', shiftKey: true })
    press({ code: 'KeyF', key: 'ƒ', altKey: true })
    press({ code: 'KeyH', key: 'H', shiftKey: true })
    press({ code: 'Comma', key: ',', metaKey: true, shiftKey: true })
    press({ code: 'Quote', key: '"', metaKey: true, shiftKey: true })
    press({ code: 'KeyC', key: 'ç', metaKey: true, altKey: true })
    press({ code: 'KeyA', key: 'A', shiftKey: true })
    press({ code: 'KeyS', key: 'S', metaKey: true, shiftKey: true })
    press({ code: 'KeyS', key: 'S', shiftKey: true })
    press({ code: 'KeyO', key: 'o', metaKey: true, ctrlKey: true })
    for (const name of Object.keys(DOCUMENT_PAGE_SHORTCUTS)) expect(fns[name as keyof DocumentPageShortcutHandlers], name).toHaveBeenCalledTimes(1)
  })

  it('is not blocked by a non-modal dialog such as the floating agent chat, but is by a modal one', () => {
    const fns = handlers()
    render(<Harness handlers={fns}/>)
    const floating = document.body.appendChild(document.createElement('section'))
    floating.setAttribute('role', 'dialog')
    floating.setAttribute('aria-modal', 'false')
    press({ code: 'KeyA', key: 'A', shiftKey: true })
    expect(fns.toggleAuthors).toHaveBeenCalledTimes(1)
    floating.removeAttribute('aria-modal')
    press({ code: 'KeyA', key: 'A', shiftKey: true })
    expect(fns.toggleAuthors).toHaveBeenCalledTimes(1)
    floating.remove()
  })

  it('stays out of text fields, open menus and dialogs, and can be disabled', () => {
    const fns = handlers()
    const { getByLabelText, rerender } = render(<Harness handlers={fns}/>)
    press({ code: 'KeyA', key: 'A', shiftKey: true }, getByLabelText('field'))
    const menu = document.body.appendChild(document.createElement('div'))
    menu.setAttribute('role', 'menu')
    press({ code: 'KeyA', key: 'A', shiftKey: true })
    menu.remove()
    expect(fns.toggleAuthors).not.toHaveBeenCalled()
    rerender(<Harness handlers={fns} enabled={false}/>)
    press({ code: 'KeyA', key: 'A', shiftKey: true })
    expect(fns.toggleAuthors).not.toHaveBeenCalled()
  })
})

function UiHarness({ id, run }: { id: string; run: Partial<Record<'history' | 'authors' | 'subscribers' | 'owner', () => void>> }) {
  useDocumentUiActions(id, run)
  return null
}

describe('useDocumentUiActions', () => {
  it('handles requests addressed to its document and reports them handled', () => {
    const history = vi.fn()
    const owner = vi.fn()
    render(<UiHarness id="document-1" run={{ history, owner }}/>)
    expect(requestDocumentUiAction('history', 'document-1')).toBe(true)
    expect(requestDocumentUiAction('owner', 'document-1')).toBe(true)
    expect(history).toHaveBeenCalledTimes(1)
    expect(owner).toHaveBeenCalledTimes(1)
  })

  it('leaves other documents and unsupported actions unhandled', () => {
    const history = vi.fn()
    render(<UiHarness id="document-1" run={{ history }}/>)
    expect(requestDocumentUiAction('history', 'document-2')).toBe(false)
    expect(requestDocumentUiAction('authors', 'document-1')).toBe(false)
    expect(history).not.toHaveBeenCalled()
  })
})
