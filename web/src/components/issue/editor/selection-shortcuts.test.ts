import { describe, expect, it, vi } from 'vitest'
import { handleSelectionShortcut } from './selection-shortcuts'

const doc = { textBetween: () => 'picked words' }
const press = (init: KeyboardEventInit) => new KeyboardEvent('keydown', { cancelable: true, ...init })

describe('selection shortcuts', () => {
  it('⌘J asks the agent about the selected text and keeps the app-wide ⌘J away', () => {
    const onAskAgent = vi.fn()
    const event = press({ key: 'j', metaKey: true })
    expect(handleSelectionShortcut(event, { from: 2, to: 8 }, doc, { onAskAgent })).toBe(true)
    expect(event.defaultPrevented).toBe(true)
    expect(onAskAgent).toHaveBeenCalledWith({ from: 2, to: 8, text: 'picked words' })
  })
  it('⌘⇧O makes an issue from the selection', () => {
    const onCreateIssue = vi.fn()
    expect(handleSelectionShortcut(press({ key: 'O', metaKey: true, shiftKey: true }), { from: 2, to: 8 }, doc, { onCreateIssue })).toBe(true)
    expect(onCreateIssue).toHaveBeenCalledTimes(1)
  })
  it('does nothing without a selection, without the action, or with other modifiers', () => {
    const onAskAgent = vi.fn()
    const event = press({ key: 'j', ctrlKey: true })
    expect(handleSelectionShortcut(event, { from: 4, to: 4 }, doc, { onAskAgent })).toBe(false)
    expect(handleSelectionShortcut(press({ key: 'j', metaKey: true }), { from: 2, to: 8 }, doc, {})).toBe(false)
    expect(handleSelectionShortcut(press({ key: 'j', metaKey: true, shiftKey: true }), { from: 2, to: 8 }, doc, { onAskAgent })).toBe(false)
    expect(handleSelectionShortcut(press({ key: 'j' }), { from: 2, to: 8 }, doc, { onAskAgent })).toBe(false)
    expect(event.defaultPrevented).toBe(false)
    expect(onAskAgent).not.toHaveBeenCalled()
  })
})
