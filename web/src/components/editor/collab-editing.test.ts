import { describe, expect, it } from 'vitest'
import { Doc } from 'yjs'
import { COLLAB_EDITING_KEYMAP, CollabEditing, createCollabUndoManager, isCollabUndoKey } from './collab-editing'

describe('CollabEditing', () => {
  it('exposes Mod-z / Shift-Mod-z / Mod-y keymap parity', () => {
    expect(COLLAB_EDITING_KEYMAP).toEqual({ undo: 'Mod-z', redo: 'Shift-Mod-z', redoAlt: 'Mod-y' })
    expect(CollabEditing.name).toBe('collabEditing')
  })

  it('creates a Yjs UndoManager on the prosemirror fragment', () => {
    const document = new Doc()
    const fragment = document.getXmlFragment('prosemirror')
    const undoManager = createCollabUndoManager(fragment)
    expect(undoManager.undoStack).toEqual([])
    undoManager.destroy()
    document.destroy()
  })

  it('detects collab undo/redo keys', () => {
    expect(isCollabUndoKey({ metaKey: true, ctrlKey: false, shiftKey: false, key: 'z' } as KeyboardEvent)).toBe('undo')
    expect(isCollabUndoKey({ metaKey: true, ctrlKey: false, shiftKey: true, key: 'z' } as KeyboardEvent)).toBe('redo')
    expect(isCollabUndoKey({ metaKey: false, ctrlKey: true, shiftKey: false, key: 'y' } as KeyboardEvent)).toBe('redo')
    expect(isCollabUndoKey({ metaKey: false, ctrlKey: false, shiftKey: false, key: 'z' } as KeyboardEvent)).toBe(false)
  })
})
