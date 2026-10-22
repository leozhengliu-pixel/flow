import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { afterEach, describe, expect, it } from 'vitest'
import { DescriptionCallout } from './callout-extension'
import { DescriptionDiagram } from './diagram-extension'
import { DescriptionFile, DescriptionVideo, insertEmbedFiles } from './file-extension'
import { structuredBlocks } from './structured-blocks'

describe('description embeds', () => {
  let editor: Editor
  afterEach(() => editor?.destroy())

  it('registers callout, diagram, file, and video nodes', () => {
    expect(DescriptionCallout.name).toBe('callout')
    expect(DescriptionDiagram.name).toBe('diagram')
    expect(DescriptionFile.name).toBe('file')
    expect(DescriptionVideo.name).toBe('video')
  })

  it('parses a cyan callout from Markdown', () => {
    editor = new Editor({
      extensions: [StarterKit, Markdown, DescriptionCallout, ...structuredBlocks],
      content: ':::callout{cyan}\nNote me\n:::',
      contentType: 'markdown',
    })
    expect(editor.getJSON().content?.[0]?.type).toBe('callout')
    expect(editor.getText()).toContain('Note me')
  })

  it('inserts file and diagram nodes', () => {
    editor = new Editor({
      extensions: [StarterKit, Markdown, DescriptionFile, DescriptionDiagram],
      content: '<p></p>',
    })
    editor.commands.setContent({
      type: 'doc',
      content: [
        { type: 'file', attrs: { src: '/uploads/spec.pdf', title: 'spec.pdf', size: 2048, contentType: 'application/pdf' } },
        { type: 'diagram', attrs: { source: 'flowchart TD\n    A-->B' } },
      ],
    })
    const types = editor.getJSON().content?.map(node => node.type)
    expect(types).toEqual(expect.arrayContaining(['file', 'diagram']))
  })

  it('renders the file card with name, size, type icon and a download action but no remove control', () => {
    const host = document.createElement('div')
    document.body.append(host)
    editor = new Editor({
      element: host,
      extensions: [StarterKit, Markdown, DescriptionFile],
      content: '::file[spec.md](/uploads/asset_1.md){size="9351" type="text/markdown"}\n\n::file[report.pdf](/uploads/asset_2.pdf){size="193" type="application/pdf"}',
      contentType: 'markdown',
    })
    const cards = host.querySelectorAll('.description-file-node')
    expect(cards).toHaveLength(2)
    expect(cards[0].querySelector('.description-file-name')?.textContent).toBe('spec.md')
    expect(cards[0].querySelector('.description-file-meta')?.textContent).toBe('9.13 KB')
    expect(cards[1].querySelector('.description-file-meta')?.textContent).toBe('193 B')
    expect(cards[0].querySelector('.description-file-download')?.getAttribute('aria-label')).toBe('Download')
    expect(cards[0].querySelector('.description-file-remove')).toBeNull()
    expect(cards[0].querySelector('a')).toBeNull()
    expect(cards[0].querySelector('.description-file-preview')).toBeNull()
    expect(cards[1].querySelector('.description-file-preview canvas')).not.toBeNull()
    expect(cards[1].classList.contains('has-preview')).toBe(true)
    expect(cards[0].querySelector('.description-file-icon path')?.getAttribute('d')).not.toBe(cards[1].querySelector('.description-file-icon path')?.getAttribute('d'))
    expect(editor.getMarkdown()).toContain('::file[spec.md](/uploads/asset_1.md){size="9351" type="text/markdown"}')
    host.remove()
  })

  it('gives a PDF card the reference toolbar (view, download, copy link, print, delete) and no other file', () => {
    const host = document.createElement('div')
    document.body.append(host)
    editor = new Editor({
      element: host,
      extensions: [StarterKit, Markdown, DescriptionFile],
      content: '::file[spec.md](/uploads/asset_1.md){size="9351" type="text/markdown"}\n\n::file[report.pdf](/uploads/asset_2.pdf){size="193" type="application/pdf"}',
      contentType: 'markdown',
    })
    const cards = host.querySelectorAll('.description-file-node')
    const labels = (card: Element) => [...card.querySelectorAll('.description-file-toolbar button')].map(button => button.getAttribute('aria-label'))
    expect(labels(cards[1])).toEqual(['View PDF', 'Download', 'Copy link', 'Print', 'Delete'])
    expect(cards[1].querySelector('.description-file-toolbar')?.hasAttribute('hidden')).toBe(false)
    expect(cards[1].querySelector('.description-file-toolbar button')?.getAttribute('data-tooltip-text')).toBe('View PDF')
    expect(cards[1].querySelector('.description-file-toolbar-separator')).not.toBeNull()
    expect(cards[0].querySelector('.description-file-toolbar')?.hasAttribute('hidden')).toBe(true)
    expect(cards[0].querySelector('.description-file-download')?.hasAttribute('hidden')).toBe(false)
    expect(cards[1].querySelector('.description-file-download')?.hasAttribute('hidden')).toBe(true)
    host.remove()
  })

  it('deletes a PDF card from its toolbar as one undoable step and omits the preview strip when configured', () => {
    const host = document.createElement('div')
    document.body.append(host)
    editor = new Editor({
      element: host,
      extensions: [StarterKit, Markdown, DescriptionFile.configure({ preview: false })],
      content: '::file[report.pdf](/uploads/asset_2.pdf){size="193" type="application/pdf"}',
      contentType: 'markdown',
    })
    expect(host.querySelector('.description-file-preview')).toBeNull()
    expect(host.querySelector('.description-file-node')?.classList.contains('has-preview')).toBe(false)
    const remove = [...host.querySelectorAll<HTMLButtonElement>('.description-file-toolbar button')].find(button => button.getAttribute('aria-label') === 'Delete')
    remove?.click()
    expect(host.querySelector('.description-file-node')).toBeNull()
    expect(editor.getJSON().content?.some(node => node.type === 'file')).toBe(false)
    editor.commands.undo()
    expect(editor.getJSON().content?.some(node => node.type === 'file')).toBe(true)
    host.remove()
  })

  it('keeps picked files in order', () => {
    const host = document.createElement('div')
    document.body.append(host)
    editor = new Editor({ element: host, extensions: [StarterKit, Markdown, DescriptionFile], content: '<p>text</p>' })
    const original = URL.createObjectURL
    let count = 0
    URL.createObjectURL = () => `blob:test/${count++}`
    try {
      insertEmbedFiles(editor.view, ['a.bin', 'b.bin', 'c.bin'].map(name => new File(['x'], name)))
    } finally {
      URL.createObjectURL = original
    }
    const names = [...host.querySelectorAll('.description-file-name')].map(node => node.textContent)
    expect(names).toEqual(['a.bin', 'b.bin', 'c.bin'])
    host.remove()
  })
})
