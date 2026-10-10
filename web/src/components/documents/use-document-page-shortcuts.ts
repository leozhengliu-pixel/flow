/**
 * Keyboard shortcuts and external UI requests of an open document page.
 * Hints use Linear's notation (⇧ P, ⌥ F, ⌘ ⇧ ,) and run unless focus is in a
 * text field (the editor included) or a menu or dialog is open.
 */
import { useEffect, useRef } from 'react'

import { useLinearHotkeys } from '@/components/ui/menu-shortcuts'
import { DOCUMENT_UI_EVENT, type DocumentUiAction, type DocumentUiActionDetail } from './document-actions'

export interface DocumentPageShortcutHandlers {
  /** ⇧ P — opens the "…" menu at Move to. */
  move: () => void
  /** ⇧ R */
  rename: () => void
  /** ⌥ F */
  favorite: () => void
  /** ⇧ H — opens the "…" menu at Remind me. */
  remind: () => void
  /** ⌘ ⇧ , */
  copyUrl: () => void
  /** ⌘ ⇧ ' */
  copyTitle: () => void
  /** ⌘ ⌥ C */
  copyMarkdown: () => void
  /** ⇧ A */
  toggleAuthors: () => void
  /** ⌘ ⇧ S — opens the subscribers popover. */
  subscribers: () => void
  /** ⇧ S — subscribe or unsubscribe. */
  toggleSubscription: () => void
  /** Ctrl ⌘ O — opens the owner picker. */
  changeOwner: () => void
}

export const DOCUMENT_PAGE_SHORTCUTS = {
  move: '⇧ P',
  rename: '⇧ R',
  favorite: '⌥ F',
  remind: '⇧ H',
  copyUrl: '⌘ ⇧ ,',
  copyTitle: "⌘ ⇧ '",
  copyMarkdown: '⌘ ⌥ C',
  toggleAuthors: '⇧ A',
  subscribers: '⌘ ⇧ S',
  toggleSubscription: '⇧ S',
  changeOwner: 'Ctrl ⌘ O',
} as const satisfies Record<keyof DocumentPageShortcutHandlers, string>

export function useDocumentPageShortcuts(handlers: DocumentPageShortcutHandlers, enabled = true) {
  const bindings: Record<string, () => void> = {}
  for (const name of Object.keys(DOCUMENT_PAGE_SHORTCUTS) as Array<keyof DocumentPageShortcutHandlers>) {
    bindings[DOCUMENT_PAGE_SHORTCUTS[name]] = () => handlers[name]()
  }
  useLinearHotkeys(bindings, enabled)
}

/**
 * Answers `requestDocumentUiAction` (⌘K, list menus, selection bar) for this
 * document: marks the request handled and runs the page-level action.
 */
export function useDocumentUiActions(documentId: string, handlers: Partial<Record<DocumentUiAction, () => void>>) {
  const latest = useRef(handlers)
  useEffect(() => { latest.current = handlers })
  useEffect(() => {
    const onRequest = (event: Event) => {
      const detail = (event as CustomEvent<DocumentUiActionDetail>).detail
      if (!detail || detail.documentId !== documentId) return
      const run = latest.current[detail.action]
      if (!run) return
      detail.handled = true
      run()
    }
    window.addEventListener(DOCUMENT_UI_EVENT, onRequest)
    return () => window.removeEventListener(DOCUMENT_UI_EVENT, onRequest)
  }, [documentId])
}
