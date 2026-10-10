import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Editor } from '@tiptap/core'
import { Markdown } from '@tiptap/markdown'
import StarterKit from '@tiptap/starter-kit'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { DescriptionCalloutSchema } from '@/components/issue/editor/callout-extension'
import { CALLOUT_ALERT_PRESETS, calloutPickerColor, normalizeCalloutColor } from './callout-model'

vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'callout-test' }))

function headless(markdown: string) {
  return new Editor({ extensions: [StarterKit, Markdown, DescriptionCalloutSchema], content: markdown, contentType: 'markdown' })
}

async function mount(value: string) {
  let editor: Editor | null = null
  render(<I18nProvider><IssueDescriptionEditor value={value} editorRef={next => { editor = next as Editor | null }}/></I18nProvider>)
  await waitFor(() => expect(document.querySelector('aside[data-callout]')).not.toBeNull())
  return () => editor as unknown as Editor
}

describe('callout markdown', () => {
  it('parses the legacy form', () => {
    const editor = headless(':::callout{green}\nHello\n:::')
    expect(editor.getJSON().content?.[0]).toMatchObject({ type: 'callout', attrs: { color: 'green', icon: '' } })
    expect(headless(':::callout\nHello\n:::').getJSON().content?.[0]?.attrs).toMatchObject({ color: 'cyan', icon: '' })
  })

  it('round-trips colour and icon', () => {
    const emoji = headless(':::callout{cyan icon="💡"}\nBody\n:::')
    expect(emoji.getJSON().content?.[0]?.attrs).toMatchObject({ color: 'cyan', icon: '💡' })
    expect(emoji.getMarkdown().trimEnd()).toBe(':::callout{cyan icon="💡"}\nBody\n:::')
    const custom = headless(':::callout{#FF8800 icon="Bell"}\nBody\n:::')
    expect(custom.getJSON().content?.[0]?.attrs).toMatchObject({ color: '#ff8800', icon: 'Bell' })
    expect(custom.getMarkdown().trimEnd()).toBe(':::callout{#ff8800 icon="Bell"}\nBody\n:::')
    expect(headless(':::callout{gray}\nBody\n:::').getMarkdown().trimEnd()).toBe(':::callout{gray}\nBody\n:::')
  })

  it('keeps several blocks and inline formatting in the body', () => {
    const editor = headless(':::callout{red}\nFirst **bold**\n\n- one\n- two\n:::')
    const content = editor.getJSON().content?.[0]?.content as { type: string; content?: { marks?: { type: string }[] }[] }[]
    expect(content?.map(node => node.type)).toEqual(['paragraph', 'bulletList'])
    expect(content?.[0]?.content?.some(node => node.marks?.[0]?.type === 'bold')).toBe(true)
    const again = headless(editor.getMarkdown())
    expect(again.getJSON()).toEqual(editor.getJSON())
  })

  it('parses an empty callout and falls back to cyan for an unknown colour', () => {
    expect(headless(':::callout{blue}\n\n:::').getJSON().content?.[0]).toMatchObject({ type: 'callout', attrs: { color: 'cyan' } })
  })

  it('maps GitHub alerts to colours and emoji', () => {
    expect(CALLOUT_ALERT_PRESETS.NOTE).toEqual({ color: 'cyan', icon: 'ℹ️' })
    expect(CALLOUT_ALERT_PRESETS.CAUTION).toEqual({ color: 'red', icon: '🛑' })
    expect(Object.keys(CALLOUT_ALERT_PRESETS)).toHaveLength(5)
  })

  it('normalises colours', () => {
    expect(normalizeCalloutColor('purple')).toBe('purple')
    expect(normalizeCalloutColor('#ABCDEF')).toBe('#abcdef')
    expect(normalizeCalloutColor('nope')).toBe('cyan')
    expect(calloutPickerColor('#ABCDEF')).toBe('#abcdef')
  })
})

describe('callout view', () => {
  beforeEach(() => {
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
  })

  it('shows the lightbulb on the icon button and a legacy colour class', async () => {
    await mount(':::callout{green}\nBody\n:::')
    const aside = document.querySelector('aside[data-callout]') as HTMLElement
    expect(aside).toHaveClass('description-callout', 'is-green')
    expect(aside.dataset.color).toBe('green')
    const button = screen.getByRole('button', { name: 'Change callout icon or color' })
    expect(button.querySelector('svg path')).not.toBeNull()
  })

  it('sets --callout-color for a custom hex colour', async () => {
    await mount(':::callout{#ff8800}\nBody\n:::')
    const aside = document.querySelector('aside[data-callout]') as HTMLElement
    expect(aside.style.getPropertyValue('--callout-color')).toBe('#ff8800')
    expect(aside).not.toHaveClass('is-orange')
  })

  it('shows an emoji icon from the markdown', async () => {
    await mount(':::callout{cyan icon="💡"}\nBody\n:::')
    expect(screen.getByRole('button', { name: 'Change callout icon or color' })).toHaveTextContent('💡')
  })

  it('opens the shared picker and writes the chosen icon, emoji and colour to the node', async () => {
    const user = userEvent.setup()
    const editor = await mount(':::callout{cyan}\nBody\n:::')
    await user.click(screen.getByRole('button', { name: 'Change callout icon or color' }))
    expect(screen.getByRole('tab', { name: 'Icons' })).toBeVisible()
    expect(screen.getByRole('tab', { name: 'Emojis' })).toBeVisible()

    await user.click(screen.getByRole('button', { name: /^Color #5e6ad2$/ }))
    await waitFor(() => expect(editor().getJSON().content?.[0]?.attrs).toMatchObject({ color: '#5e6ad2', icon: '' }))

    await user.click(screen.getByRole('button', { name: 'Automation' }))
    await waitFor(() => expect(editor().getJSON().content?.[0]?.attrs).toMatchObject({ icon: 'Automation', color: '#5e6ad2' }))
    expect(editor().getMarkdown()).toContain(':::callout{#5e6ad2 icon="Automation"}')

    await user.click(screen.getByRole('button', { name: 'Change callout icon or color' }))
    await user.click(screen.getByRole('tab', { name: 'Emojis' }))
    await user.click(screen.getByRole('button', { name: 'tada' }))
    await waitFor(() => expect(editor().getJSON().content?.[0]?.attrs).toMatchObject({ icon: '🎉' }))
    const aside = document.querySelector('aside[data-callout]') as HTMLElement
    expect(aside.style.getPropertyValue('--callout-color')).toBe('#5e6ad2')
  })
})
