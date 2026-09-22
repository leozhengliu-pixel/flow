import { describe, expect, it } from 'vitest'
import {
  LazyFastTriageAcceptEditor,
  preloadFastTriageAcceptEditor,
} from './lazy-fast-triage-accept-editor-loader'

describe('LazyFastTriageAcceptEditorLoader (LS-0374)', () => {
  it('exposes preload that resolves the editor module', async () => {
    expect(typeof LazyFastTriageAcceptEditor.preload).toBe('function')
    await expect(preloadFastTriageAcceptEditor()).resolves.toBeUndefined()
  })
})
