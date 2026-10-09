/* eslint-disable react/only-export-components -- a test helper module, never hot reloaded */
import { act } from '@testing-library/react'
import type { ReactNode } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { WorkspaceStoreProvider } from '@/store/application-store-context'
import type { BootstrapData } from '@/types/flow'

/** Workspace, router and i18n around a host component whose rich text fields support mentions. */
export function MentionShell({ children, data }: { children: ReactNode; data: BootstrapData }) {
  return <I18nProvider><MemoryRouter><WorkspaceStoreProvider account={null} data={data} session={null}>{children}</WorkspaceStoreProvider></MemoryRouter></I18nProvider>
}

/** The browser APIs ProseMirror asks for that jsdom lacks. Call from `beforeEach`; undo with `vi.unstubAllGlobals()`. */
export function stubEditorEnvironment() {
  vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
  Range.prototype.getClientRects = () => [] as unknown as DOMRectList
  Range.prototype.getBoundingClientRect = () => new DOMRect()
  document.elementFromPoint = () => document.body
}

/** Pastes plain text into an editor element the way the browser does. */
export function pasteText(element: Element, text: string) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: { types: ['text/plain'], files: [], items: [], getData: (type: string) => type === 'text/plain' ? text : '' } })
  act(() => { element.dispatchEvent(event) })
}
