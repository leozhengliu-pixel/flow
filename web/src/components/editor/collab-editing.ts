/**
 * LS-0119 CollabEditing — Yjs UndoManager + Mod-z / Shift-Mod-z / Mod-y
 * while TipTap Collaboration (yUndoPlugin) stays the sync host.
 */
import type { CSSProperties } from 'react'
import { Extension } from '@tiptap/core'
import { Plugin, PluginKey, type EditorState, type Transaction } from '@tiptap/pm/state'
import { UndoManager, type XmlFragment } from 'yjs'
import { updateYFragment, ySyncPluginKey, yUndoPluginKey } from '@tiptap/y-tiptap'

/** The reference app CollabEditing keymap parity. TipTap Collaboration registers the same shortcuts; this module is the named surface + helpers. */
export const COLLAB_EDITING_KEYMAP = {
  undo: 'Mod-z',
  redo: 'Shift-Mod-z',
  redoAlt: 'Mod-y',
} as const

export const REMOTE_SELECTION_CLASS = 'remote-selection'
export const REMOTE_CURSOR_CLASS = 'collaboration-carets__caret'

export interface CollabUndoManagerOptions {
  captureTimeout?: number
  trackedOrigins?: unknown[]
}

/** Create a Yjs UndoManager bound to a prosemirror XmlFragment (the reference app CollabEditing spirit). */
export function createCollabUndoManager(fragment: XmlFragment, options: CollabUndoManagerOptions = {}) {
  return new UndoManager(fragment, {
    trackedOrigins: new Set([ySyncPluginKey, ...(options.trackedOrigins ?? [])]),
    captureTimeout: options.captureTimeout ?? 500,
  })
}

export function isCollabUndoKey(event: KeyboardEvent) {
  const mod = event.metaKey || event.ctrlKey
  if (!mod) return false
  const key = event.key.toLowerCase()
  if (key === 'z' && !event.shiftKey) return 'undo' as const
  if (key === 'z' && event.shiftKey) return 'redo' as const
  if (key === 'y' && !event.shiftKey) return 'redo' as const
  return false
}

/**
 * Named TipTap extension surface for CollabEditing keymap parity.
 * TipTap Collaboration already installs yUndoPlugin + these shortcuts; this
 * extension documents the reference app CollabEditing surface and re-asserts the keymap
 * so Mod-z / Shift-Mod-z / Mod-y remain bound when StarterKit undoRedo is off.
 */
export const CollabEditing = Extension.create({
  name: 'collabEditing',
  priority: 1100,
  addKeyboardShortcuts() {
    return {
      [COLLAB_EDITING_KEYMAP.undo]: () => this.editor.commands.undo(),
      [COLLAB_EDITING_KEYMAP.redo]: () => this.editor.commands.redo(),
      [COLLAB_EDITING_KEYMAP.redoAlt]: () => this.editor.commands.redo(),
    }
  },
})

/**
 * Writes a local text-only edit to the shared Yjs fragment right away, before other plugins append rewrites to it.
 *
 * The Yjs binding normally writes in its view update, after the whole transaction chain. For a structural edit it
 * carries a position plan into the appended transactions, but a plain text edit leaves none, so a structural rewrite
 * appended to it (the link and Markdown paste rules after a paste, autolink after a space) maps positions against a
 * shared fragment that does not hold the new text yet. When that text has no shared text node yet (an empty paragraph,
 * after a mention chip or a line break) the binding throws "Position tracking requires synchronized editor text" and the
 * edit is dropped. Returns true when it wrote.
 */
export function syncLocalTextEditBeforeAppend(transactions: readonly Transaction[], state: EditorState) {
  const sync = ySyncPluginKey.getState(state)
  if (!sync) return false
  const { binding } = sync
  if (!binding.prosemirrorView || binding.projectionError || sync.snapshot || sync.prevSnapshot) return false
  if (!transactions.some(transaction => transaction.docChanged)) return false
  // Remote updates and Yjs undo/redo are rendered from the shared fragment, which already holds them.
  if (transactions.some(transaction => transaction.getMeta(ySyncPluginKey)?.isChangeOrigin)) return false
  // A structural edit's pending plan already covers the appended transactions.
  if (sync.positionMigration || binding.syncedDoc === state.doc) return false
  // Like the binding, never write the schema's empty placeholder document before shared content arrives.
  const empty = state.doc.type.createAndFill()
  if (empty && state.doc.content.findDiffStart(empty.content) === null) return false
  if (!sync.addToHistory) yUndoPluginKey.getState(state)?.undoManager?.stopCapturing()
  let wrote = false
  binding.mux(() => {
    sync.doc.transact(transaction => {
      transaction.meta.set('addToHistory', sync.addToHistory)
      updateYFragment(sync.doc, binding.type, state.doc, binding)
      binding.syncedDoc = state.doc
      wrote = true
    }, ySyncPluginKey)
  })
  return wrote
}

const syncBeforeAppendKey = new PluginKey('flowCollabSyncBeforeAppend')

/**
 * Keeps the shared Yjs fragment in step with local text edits before any other plugin appends a transaction (see
 * `syncLocalTextEditBeforeAppend`). Its priority puts its plugin ahead of every other appendTransaction plugin.
 */
export const CollabSyncBeforeAppend = Extension.create({
  name: 'collabSyncBeforeAppend',
  priority: 100_000,
  addProseMirrorPlugins() {
    return [new Plugin({
      key: syncBeforeAppendKey,
      appendTransaction: (transactions, _oldState, newState) => {
        syncLocalTextEditBeforeAppend(transactions, newState)
        return null
      },
    })]
  },
})

export function remoteSelectionStyle(color: string): CSSProperties {
  return {
    ['--selection-color' as string]: color,
    ['--border-color' as string]: color,
  }
}
