import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  clearDocumentContent,
  getDocumentContent,
  getDocumentContentState,
  notifyListenerPresence,
  resetDocumentContentEditorState,
  setDocumentContent,
  subscribeToListenerPresence,
} from './document-content-editor-state'

afterEach(() => resetDocumentContentEditorState())

describe('DocumentContentEditorState', () => {
  it('stores content and contentState by document id', () => {
    setDocumentContent('doc-1', '# Hello', '{"type":"doc"}')
    expect(getDocumentContent('doc-1')).toBe('# Hello')
    expect(getDocumentContentState('doc-1')).toBe('{"type":"doc"}')
  })

  it('notifies presence listeners on set and explicit notify', () => {
    const listener = vi.fn()
    const unsubscribe = subscribeToListenerPresence('doc-2', listener)
    setDocumentContent('doc-2', 'body')
    expect(listener).toHaveBeenCalledWith('doc-2')
    notifyListenerPresence('doc-2')
    expect(listener).toHaveBeenCalledTimes(2)
    unsubscribe()
    setDocumentContent('doc-2', 'next')
    expect(listener).toHaveBeenCalledTimes(2)
  })

  it('clears stored content', () => {
    setDocumentContent('doc-3', 'x', 'y')
    clearDocumentContent('doc-3')
    expect(getDocumentContent('doc-3')).toBeUndefined()
    expect(getDocumentContentState('doc-3')).toBeUndefined()
  })
})
