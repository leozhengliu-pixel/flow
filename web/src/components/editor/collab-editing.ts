/**
 * LS-0119 CollabEditing — Yjs UndoManager + Mod-z / Shift-Mod-z / Mod-y
 * while TipTap Collaboration (yUndoPlugin) stays the sync host.
 */
import type { CSSProperties } from 'react'
import { Extension } from '@tiptap/core'
import { UndoManager, type XmlFragment } from 'yjs'
import { ySyncPluginKey } from '@tiptap/y-tiptap'

/** Linear CollabEditing keymap parity. TipTap Collaboration registers the same shortcuts; this module is the named surface + helpers. */
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

/** Create a Yjs UndoManager bound to a prosemirror XmlFragment (Linear CollabEditing spirit). */
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
 * extension documents the Linear CollabEditing surface and re-asserts the keymap
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

export function remoteSelectionStyle(color: string): CSSProperties {
  return {
    ['--selection-color' as string]: color,
    ['--border-color' as string]: color,
  }
}
