import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { afterEach, beforeAll, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { SelectionToolbar } from './selection-toolbar'
import { structuredBlocks } from './structured-blocks'

let editor: Editor
beforeAll(() => {
  Object.defineProperty(Range.prototype,'getClientRects',{configurable:true,value:()=>[]})
  Object.defineProperty(Range.prototype,'getBoundingClientRect',{configurable:true,value:()=>new DOMRect(0,0,1,1)})
})
afterEach(() => editor?.destroy())
function setup() {
  editor = new Editor({ extensions:[StarterKit,Markdown,...structuredBlocks], content:'<p>Selected requirement</p>' })
  editor.commands.setTextSelection({from:1,to:21})
  const actions = {onCreateIssue:vi.fn(),onAskAgent:vi.fn(),onComment:vi.fn()}
  render(<I18nProvider><TooltipProvider><SelectionToolbar editor={editor} actions={actions}/></TooltipProvider></I18nProvider>)
  return actions
}
it('offers all 14 controls and passes the selected text with its range', async () => {
  const actions=setup(),user=userEvent.setup()
  expect(screen.getAllByRole('button')).toHaveLength(14)
  for(const [label,callback] of [['Create issue from selection',actions.onCreateIssue],['Ask agent',actions.onAskAgent],['Comment',actions.onComment]] as const) {
    await user.click(screen.getByRole('button',{name:label}))
    expect(callback).toHaveBeenCalledWith({text:'Selected requirement',from:1,to:21})
  }
})
it('creates numbered lists and checklists that survive Markdown round trips', async () => {
  setup();const user=userEvent.setup()
  await user.click(screen.getByRole('button',{name:'List style'}))
  await user.click(screen.getByRole('option',{name:'Numbered list'}))
  expect(editor.isActive('orderedList')).toBe(true)
  act(()=>editor.commands.setContent('<p>Selected requirement</p>'))
  await user.click(screen.getByRole('button',{name:'List style'}))
  await user.click(screen.getByRole('option',{name:'Checklist'}))
  expect(editor.getJSON().content?.[0].type).toBe('taskList')
  const markdown=editor.getMarkdown()
  act(()=>editor.commands.setContent(markdown,{contentType:'markdown'}))
  expect(editor.getJSON().content?.[0].type).toBe('taskList')
  expect(editor.getText()).toContain('Selected requirement')
})
it('collapses selected content without deleting it and persists the block structure', async () => {
  setup();await userEvent.click(screen.getByRole('button',{name:'Collapse'}))
  expect(editor.getJSON().content?.[0].type).toBe('details')
  expect(editor.getText()).toContain('Selected requirement')
  const markdown=editor.getMarkdown()
  expect(markdown).toContain('<details')
  act(()=>editor.commands.setContent(markdown,{contentType:'markdown'}))
  expect(editor.getJSON().content?.[0].type).toBe('details')
  expect(editor.getText()).toContain('Selected requirement')
})
