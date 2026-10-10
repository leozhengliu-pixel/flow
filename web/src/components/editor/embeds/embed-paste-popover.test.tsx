import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Editor } from '@tiptap/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'

vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'popover-test' }))

function paste(editor: Editor, text: string) {
  const event = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(event, 'clipboardData', { value: { types: ['text/plain'], files: [], items: [], getData: (type: string) => type === 'text/plain' ? text : '' } })
  act(() => { editor.view.dom.dispatchEvent(event) })
}

async function press(editor: Editor, keys: string) {
  act(() => { editor.view.dom.focus() })
  await userEvent.setup().keyboard(keys)
}

async function mount(value = '') {
  let editor: Editor | null = null
  render(<I18nProvider><IssueDescriptionEditor value={value} editorRef={next => { editor = next }}/></I18nProvider>)
  await waitFor(() => expect(editor).not.toBeNull())
  return () => editor as Editor
}

const popover = () => document.querySelector<HTMLElement>('[data-embed-hint]')
const rows = () => [...document.querySelectorAll<HTMLElement>('[data-embed-hint-action]')]
const gone = () => waitFor(() => expect(popover()).toBeNull())

/** Where the caret is, in the viewport (jsdom has no layout, so the editor reports it). */
function caretAt(editor: Editor, rect: { left: number; right: number; top: number; bottom: number }) {
  vi.spyOn(editor.view, 'coordsAtPos').mockReturnValue(rect)
}

describe('paste popover (Embed ... / Keep as link)', () => {
  beforeEach(() => {
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1225 })
  })
  afterEach(() => vi.restoreAllMocks())

  it('opens as a floating menu in the document body, not inline under the link, with the keys on the right', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://www.figma.com/design/AbC123/Flow-app')
    await waitFor(() => expect(popover()).not.toBeNull())
    expect(popover()?.parentElement).toBe(document.body)
    expect(popover()?.closest('.ProseMirror')).toBeNull()
    expect(rows().map(row => row.textContent)).toEqual(['Embed previewTab', 'Keep as linkEsc'])
    expect(rows().map(row => row.dataset.active)).toEqual(['true', 'false'])
    expect(rows().map(row => row.querySelector('svg') !== null)).toEqual([true, true])
    await waitFor(() => expect(popover()?.dataset.open).toBe('true'))
  })

  it('puts the popover above the caret when 340px fit there, right-aligned to it with a 4px gap', async () => {
    const editor = await mount()
    editor().commands.focus()
    caretAt(editor(), { left: 596, right: 596, top: 468.5, bottom: 486.5 })
    vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(170)
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(76)
    paste(editor(), 'https://youtu.be/dQw4w9WgXcQ')
    await waitFor(() => expect(popover()).not.toBeNull())
    expect(popover()?.dataset.placement).toBe('top-end')
    expect(popover()?.style.transform).toBe(`translate3d(${596 - 170}px, ${468.5 - 4 - 76}px, 0)`)
  })

  it('puts it to the right of the caret near the top of the page, and below it when the editor column has no room', async () => {
    const editor = await mount()
    editor().commands.focus()
    vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(187)
    vi.spyOn(HTMLElement.prototype, 'offsetHeight', 'get').mockReturnValue(76)
    vi.spyOn(editor().view.dom, 'getBoundingClientRect').mockReturnValue(new DOMRect(328, 206, 805, 400))
    caretAt(editor(), { left: 806, right: 806, top: 219.5, bottom: 237.5 })
    paste(editor(), 'https://miro.com/app/board/uXjVKabc123=/')
    await waitFor(() => expect(popover()).not.toBeNull())
    expect(popover()?.dataset.placement).toBe('right-start')
    expect(popover()?.style.transform).toBe('translate3d(810px, 219.5px, 0)')
    await press(editor(), '{Escape}')
    await gone()

    caretAt(editor(), { left: 927, right: 927, top: 260, bottom: 278 })
    await press(editor(), '{Enter}')
    paste(editor(), 'https://miro.com/app/board/uXjVKabc123=/')
    await waitFor(() => expect(popover()).not.toBeNull())
    expect(popover()?.dataset.placement).toBe('bottom-end')
    expect(popover()?.style.transform).toBe(`translate3d(${927 - 187}px, ${278 + 4}px, 0)`)
  })

  it('embeds with Tab, keeps the link with Esc, and either closes the popover', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://www.figma.com/design/AbC123/Flow-app')
    await waitFor(() => expect(popover()).not.toBeNull())
    await press(editor(), '{Tab}')
    await gone()
    expect(editor().getJSON().content?.map(node => node.type)).toEqual(['embed', 'paragraph'])

    const second = await mount()
    second().commands.focus()
    paste(second(), 'https://www.figma.com/design/AbC123/Flow-app')
    await waitFor(() => expect(popover()).not.toBeNull())
    await press(second(), '{Escape}')
    await gone()
    expect(second().getJSON().content?.map(node => node.type)).toEqual(['paragraph'])
    expect(second().getMarkdown()).toContain('[https://www.figma.com/design/AbC123/Flow-app](https://www.figma.com/design/AbC123/Flow-app)')
  })

  it('moves the highlight with the arrow keys and activates the highlighted row with Enter', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://youtu.be/dQw4w9WgXcQ')
    await waitFor(() => expect(popover()).not.toBeNull())
    await press(editor(), '{ArrowDown}')
    await waitFor(() => expect(rows().map(row => row.dataset.active)).toEqual(['false', 'true']))
    await press(editor(), '{ArrowUp}')
    await waitFor(() => expect(rows().map(row => row.dataset.active)).toEqual(['true', 'false']))
    await press(editor(), '{ArrowDown}{Enter}')
    await gone()
    expect(editor().getJSON().content?.map(node => node.type)).toEqual(['paragraph'])

    const second = await mount()
    second().commands.focus()
    paste(second(), 'https://youtu.be/dQw4w9WgXcQ')
    await waitFor(() => expect(popover()).not.toBeNull())
    await press(second(), '{Enter}')
    await gone()
    expect(second().getJSON().content?.map(node => node.type)).toEqual(['embed', 'paragraph'])
  })

  it('highlights the row under the pointer and embeds or keeps with a click', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://youtu.be/dQw4w9WgXcQ')
    await waitFor(() => expect(popover()).not.toBeNull())
    const user = userEvent.setup()
    await user.hover(rows()[1])
    await waitFor(() => expect(rows().map(row => row.dataset.active)).toEqual(['false', 'true']))
    await user.click(rows()[1])
    await gone()
    expect(document.querySelector('iframe')).toBeNull()
    expect(editor().getJSON().content?.map(node => node.type)).toEqual(['paragraph'])

    const second = await mount()
    second().commands.focus()
    paste(second(), 'https://youtu.be/dQw4w9WgXcQ')
    await user.click(await screen.findByRole('button', { name: /Embed video/ }))
    await gone()
    expect(second().getJSON().content?.map(node => node.type)).toEqual(['embed', 'paragraph'])
  })

  it('goes away when the author types, and then Tab does nothing special', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://youtu.be/dQw4w9WgXcQ')
    await waitFor(() => expect(popover()).not.toBeNull())
    await press(editor(), 'x')
    await gone()
    await press(editor(), '{Tab}')
    expect(editor().getJSON().content?.some(node => node.type === 'embed')).toBe(false)
  })

  it('keeps the link when focus leaves the editor (clicking away)', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://youtu.be/dQw4w9WgXcQ')
    await waitFor(() => expect(popover()).not.toBeNull())
    const outside = document.createElement('button')
    document.body.append(outside)
    act(() => { editor().view.dom.focus() })
    expect(document.activeElement).toBe(editor().view.dom)
    act(() => { outside.focus() })
    await gone()
    expect(editor().getJSON().content?.map(node => node.type)).toEqual(['paragraph'])
    outside.remove()
  })

  it('is removed with the editor', async () => {
    const editor = await mount()
    editor().commands.focus()
    paste(editor(), 'https://youtu.be/dQw4w9WgXcQ')
    await waitFor(() => expect(popover()).not.toBeNull())
    act(() => { editor().destroy() })
    await waitFor(() => expect(document.querySelector('[class*="popover"]')).toBeNull())
  })
})
