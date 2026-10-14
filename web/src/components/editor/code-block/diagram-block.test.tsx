import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import Document from '@tiptap/extension-document'
import Paragraph from '@tiptap/extension-paragraph'
import Text from '@tiptap/extension-text'
import { Markdown } from '@tiptap/markdown'
import { EditorContent, useEditor, type Editor } from '@tiptap/react'
import { useEffect } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FlowCodeBlock } from './code-block-extension'
import { highlightCode } from './code-block-highlight'
import { languageLabel, resolveLanguage } from './languages'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value }) }))
vi.mock('@/lib/toast', () => ({ toast: { success: vi.fn(), error: vi.fn() } }))
const renderDiagram = vi.fn(async (source: string) => source.includes('-->')
  ? { svg: '<svg data-testid="diagram-svg" style="max-width: 120px"></svg>', width: 120 }
  : { error: 'No diagram type detected matching given configuration for text: ' + source })
vi.mock('./diagram-render', () => ({ renderDiagram: (source: string) => renderDiagram(source), copyDiagramImage: vi.fn() }))

function Harness({ content, onReady }: { content: string; onReady: (editor: Editor) => void }) {
  const editor = useEditor({ extensions: [Document, Paragraph, Text, Markdown, FlowCodeBlock], content, contentType: 'markdown' })
  useEffect(() => { if (editor) onReady(editor) }, [editor, onReady])
  return <EditorContent editor={editor}/>
}

async function mount(content: string) {
  let editor: Editor | null = null
  const view = render(<Harness content={content} onReady={next => { editor = next }}/>)
  await waitFor(() => expect(editor).not.toBeNull())
  return { ...view, editor: editor as unknown as Editor }
}

describe('Mermaid diagram blocks', () => {
  beforeEach(() => {
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
    renderDiagram.mockClear()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('treats Mermaid as a picker language that is never highlighted', () => {
    expect(resolveLanguage('mermaid')).toBe('mermaid')
    expect(languageLabel('mermaid')).toBe('Mermaid')
    expect(highlightCode('flowchart LR\n  A --> B', 'mermaid')).toEqual([])
  })

  it('renders a ```mermaid fence as the diagram with the reference app\'s toolbar, and round-trips to the same fence', async () => {
    const { editor, container } = await mount('```mermaid\nflowchart LR\n  A --> B\n```')
    expect(await screen.findByTestId('diagram-svg')).toBeInTheDocument()
    expect(container.querySelector('.flow-diagram')).not.toHaveAttribute('data-source-visible')
    for (const label of ['Copy diagram', 'Show source', 'Fullscreen']) expect(screen.getByRole('button', { name: label })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Copy code' })).toBeNull()
    expect(editor.getJSON().content?.[0]).toMatchObject({ type: 'codeBlock', attrs: { language: 'mermaid' } })
    expect(editor.getMarkdown()).toContain('```mermaid\nflowchart LR\n  A --> B\n```')
  })

  it('shows the source on demand and, for invalid source, the error marker and console', async () => {
    const user = userEvent.setup()
    const { container } = await mount('```mermaid\nnot a diagram\n```')
    await waitFor(() => expect(renderDiagram).toHaveBeenCalled())
    await user.click(await screen.findByRole('button', { name: 'Show source' }))
    expect(container.querySelector('.flow-diagram')).toHaveAttribute('data-source-visible')
    expect(await screen.findByRole('button', { name: 'Show error' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Error in diagram. Click to view' }))
    expect(container.querySelector('.flow-diagram__console')).toHaveTextContent('No diagram type detected')
  })

  it('shows the placeholder for an empty diagram and disables the actions that need content', async () => {
    await mount('```mermaid\n```')
    expect(await screen.findByText('Your diagram will display here')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'No diagram to copy' })).toHaveAttribute('aria-disabled', 'true')
    expect(renderDiagram).not.toHaveBeenCalled()
  })

  it('switches a code block to the diagram when its language becomes Mermaid', async () => {
    const { editor } = await mount('```\nflowchart LR\n  A --> B\n```')
    expect(screen.getByRole('button', { name: 'Copy code' })).toBeInTheDocument()
    act(() => { editor.chain().setTextSelection(1).updateAttributes('codeBlock', { language: 'mermaid' }).run() })
    expect(await screen.findByTestId('diagram-svg')).toBeInTheDocument()
  })
})
