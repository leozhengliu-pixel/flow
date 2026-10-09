import { act, waitFor } from '@testing-library/react'
import type { UserEvent } from '@testing-library/user-event'
import { expect, vi } from 'vitest'

/** The jsdom gaps the rich editor trips over (layout observers and Range geometry). */
export function stubEditorDom() {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
  Range.prototype.getClientRects = () => [] as unknown as DOMRectList
  Range.prototype.getBoundingClientRect = () => new DOMRect()
  document.elementFromPoint = () => document.body
}

export function paste(element: Element, text: string) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: { types: ['text/plain'], files: [], items: [], getData: (type: string) => type === 'text/plain' ? text : '' } })
  act(() => { element.dispatchEvent(event) })
}

/**
 * Types "@Launch" in the field and picks the "Launch plan" document. Fields inside a Radix modal dialog cannot be clicked
 * through (the dialog makes the body inert), so those pick the option with the keyboard.
 */
export async function pickDocument(user: UserEvent, box: HTMLElement, { keyboard = false } = {}) {
  await user.click(box)
  await user.keyboard('See @Launch')
  // A plain DOM query: role queries are slow on these big settings pages and would run past the wait.
  const option = await waitFor(() => {
    const found = [...document.querySelectorAll<HTMLElement>('[role="option"]')].find(item => item.textContent?.includes('Launch plan'))
    expect(found).toBeTruthy()
    return found as HTMLElement
  }, { timeout: 4000 })
  if (keyboard) await user.keyboard('{Enter}')
  else await user.click(option)
  await waitFor(() => expect(document.querySelector('a[data-agent-entity="document"]')).toHaveTextContent('Launch plan'))
}

export const documentMarkdown = '[Launch plan](/workspace/document/plan-abc)'
