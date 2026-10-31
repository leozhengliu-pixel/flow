import { act, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { TooltipProvider } from '@/components/ui/tooltip'
import { SelectionToolbar } from './selection-toolbar'
import { structuredBlocks } from './structured-blocks'
import type { DescriptionSelectionActions } from './structured-blocks'

let editor: Editor
beforeAll(() => {
  Object.defineProperty(Range.prototype,'getClientRects',{configurable:true,value:()=>[]})
  Object.defineProperty(Range.prototype,'getBoundingClientRect',{configurable:true,value:()=>new DOMRect(0,0,1,1)})
})
afterEach(() => editor?.destroy())
function setup({ content = '<p>Selected requirement</p>', actions = {onCreateIssue:vi.fn(),onAskAgent:vi.fn(),onComment:vi.fn()} as DescriptionSelectionActions, editable = true } = {}) {
  editor = new Editor({ extensions:[StarterKit,Markdown,...structuredBlocks], content, editable })
  editor.commands.setTextSelection({from:1,to:21})
  const view = render(<I18nProvider><TooltipProvider><SelectionToolbar editor={editor} actions={actions}/></TooltipProvider></I18nProvider>)
  return { actions, view }
}
const toolbar = () => screen.getByRole('toolbar')
const buttonLabels = () => within(toolbar()).getAllByRole('button').map(button => button.getAttribute('aria-label'))
/** The toolbar's items in order: a button's glyph name (`agentPointer` for the agent), `|` for the hairline. */
const layout = () => [...toolbar().children].map(item => item.getAttribute('role') === 'separator' ? '|' : item.querySelector('svg')?.getAttribute('data-toolbar-glyph') ?? item.querySelector('svg')?.getAttribute('data-agent-glyph'))

const FORMATTING = ['Regular text','Bold','Italic','Strikethrough','Underline','Link','Quote','Collapse','Inline code','Code block','List']
const GLYPHS = ['textStyle','bold','italic','strikethrough','underline','link','quote','collapse','inlineCode','codeBlock','bulletList']

describe('selection toolbar icon set per surface', () => {
  it('documents and issue descriptions: 11 formatting buttons, one hairline, create issue / ask agent / comment', () => {
    setup()
    expect(buttonLabels()).toEqual([...FORMATTING,'Create issue from selection','Ask agent','Comment'])
    expect(layout()).toEqual([...GLYPHS,'|','createIssue','agentPointer','comment'])
    // Marks are toggles; the selection actions are plain buttons.
    expect(screen.getByRole('button',{name:'Italic'})).toHaveAttribute('aria-pressed','false')
    for (const label of ['Create issue from selection','Ask agent','Comment']) expect(screen.getByRole('button',{name:label})).not.toHaveAttribute('aria-pressed')
  })
  it('project and initiative descriptions and comments: no comment button', () => {
    setup({ actions:{onCreateIssue:vi.fn(),onAskAgent:vi.fn()} })
    expect(layout()).toEqual([...GLYPHS,'|','createIssue','agentPointer'])
  })
  it('editors without selection actions (updates, templates, composers) show formatting only, with no hairline', () => {
    setup({ actions:{} })
    expect(buttonLabels()).toEqual(FORMATTING)
    expect(layout()).toEqual(GLYPHS)
  })
  it('a read-only document keeps only the selection actions', () => {
    setup({ editable:false })
    expect(layout()).toEqual(['createIssue','agentPointer','comment'])
  })
  it('a selected heading swaps "Aa" for its H glyph, names it, and disables bold and lists', async () => {
    setup({ content:'<h2>Selected requirement</h2>' })
    const style = screen.getByRole('button',{name:'Heading 2'})
    expect(style.querySelector('svg')).toHaveAttribute('data-toolbar-glyph','heading2')
    expect(screen.getByRole('button',{name:'Bold'})).toHaveAttribute('aria-disabled','true')
    expect(screen.getByRole('button',{name:'List'})).toHaveAttribute('aria-disabled','true')
    expect(screen.getByRole('button',{name:'Italic'})).not.toHaveAttribute('aria-disabled')
    await userEvent.click(screen.getByRole('button',{name:'Bold'}))
    expect(editor.isActive('bold')).toBe(false)
  })
})

describe('selection toolbar tooltips', () => {
  const tooltipOf = (label: string) => {
    act(() => { fireEvent.focus(screen.getByRole('button',{name:label})) })
    const tip = screen.getByRole('tooltip')
    const text = tip.textContent
    act(() => { fireEvent.blur(screen.getByRole('button',{name:label})) })
    return text
  }
  it('names every button with its shortcut, above the button', () => {
    setup()
    expect(Object.fromEntries([...FORMATTING,'Create issue from selection','Ask agent','Comment'].map(label => [label, tooltipOf(label)]))).toEqual({
      'Regular text':'Regular text⌘⌥0', Bold:'Bold⌘B', Italic:'Italic⌘I', Strikethrough:'Strikethrough⌘⇧X', Underline:'Underline⌘U', Link:'Link⌘K',
      Quote:'Quote⌥⇧.', Collapse:'Collapse⌘⇧6', 'Inline code':'Inline code⌘E', 'Code block':'Code block⌘⇧\\', List:'List',
      'Create issue from selection':'Create issue from selection⌘⇧O', 'Ask agent':'Ask agent⌘J', Comment:'Comment⌘⌥M',
    })
    act(() => { fireEvent.focus(screen.getByRole('button',{name:'Bold'})) })
    expect(document.querySelector('[data-radix-popper-content-wrapper] [data-side]')).toHaveAttribute('data-side','top')
  })
  it('renders the text-style tooltip through the shared tooltip, not a native title', () => {
    setup()
    for (const button of within(toolbar()).getAllByRole('button')) expect(button).not.toHaveAttribute('title')
  })
})

describe('selection toolbar behaviour', () => {
  it('passes the selected text with its range, and the editor it is in, to the selection actions', async () => {
    const { actions } = setup(), user = userEvent.setup()
    for(const [label,callback] of [['Create issue from selection',actions.onCreateIssue],['Ask agent',actions.onAskAgent],['Comment',actions.onComment]] as const) {
      await user.click(screen.getByRole('button',{name:label}))
      expect(callback).toHaveBeenCalledWith({text:'Selected requirement',from:1,to:21}, expect.objectContaining({ commands: expect.anything() }))
    }
  })
  it('shows and invokes moving the selection when a document provides the action', async () => {
    const onMoveToNewDocument = vi.fn()
    setup({ actions: { onCreateIssue: vi.fn(), onMoveToNewDocument, onAskAgent: vi.fn(), onComment: vi.fn() } })
    await userEvent.click(screen.getByRole('button', { name: 'Move selection to new document' }))
    expect(onMoveToNewDocument).toHaveBeenCalledWith({ text: 'Selected requirement', from: 1, to: 21 }, expect.objectContaining({ commands: expect.anything() }))
  })
  it('marks active formatting as pressed', async () => {
    setup(); const user = userEvent.setup()
    expect(screen.getByRole('button',{name:'Bold'})).toHaveAttribute('aria-pressed','false')
    await user.click(screen.getByRole('button',{name:'Bold'}))
    expect(editor.isActive('bold')).toBe(true)
    expect(screen.getByRole('button',{name:'Bold'})).toHaveAttribute('aria-pressed','true')
  })
  it('creates numbered lists and checklists that survive Markdown round trips', async () => {
    setup();const user=userEvent.setup()
    await user.click(screen.getByRole('button',{name:'List'}))
    expect(screen.getAllByRole('menuitemradio').map(item => item.textContent)).toEqual(['List','Numbered list','Checklist'])
    await user.click(screen.getByRole('menuitemradio',{name:'Numbered list'}))
    expect(editor.isActive('orderedList')).toBe(true)
    act(()=>editor.commands.setContent('<p>Selected requirement</p>'))
    await user.click(screen.getByRole('button',{name:'List'}))
    await user.click(screen.getByRole('menuitemradio',{name:'Checklist'}))
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
  it('lists block shortcuts in the text-style menu and switches the block', async () => {
    setup({ actions:{onAskAgent:vi.fn()} })
    const user = userEvent.setup()
    await user.click(screen.getByRole('button',{name:'Regular text'}))
    const menu = screen.getByRole('menu',{name:'Text style'})
    const options = within(menu).getAllByRole('menuitemradio')
    expect(options.map(option => option.textContent)).toEqual(['Regular text⌘⌥0','Heading 1⌘⌥1','Heading 2⌘⌥2','Heading 3⌘⌥3','Heading 4⌘⌥4'])
    expect(options[4]).toHaveAttribute('data-block','h4')
    expect(options[0]).toHaveAttribute('aria-checked','true')
    expect(options[0].querySelector('[data-flow-glyph="check"]')).not.toBeNull()
    await user.click(options[2])
    expect(editor.isActive('heading',{level:2})).toBe(true)
    await user.click(screen.getByRole('button',{name:'Heading 2'}))
    await user.click(screen.getAllByRole('menuitemradio')[4])
    expect(editor.isActive('heading',{level:4})).toBe(true)
    expect(screen.getByRole('button',{name:'Heading 4'})).toBeVisible()
  })
  it('⌘K with text selected opens the link field and keeps the app-wide menu closed', () => {
    setup()
    const event = new KeyboardEvent('keydown',{key:'k',metaKey:true,bubbles:true,cancelable:true})
    act(() => { editor.view.dom.dispatchEvent(event) })
    expect(event.defaultPrevented).toBe(true)
    expect(screen.getByRole('textbox',{name:'Link URL'})).toBeInTheDocument()
    expect(screen.getByRole('button',{name:'Link'})).toHaveAttribute('aria-pressed','true')
  })
})
