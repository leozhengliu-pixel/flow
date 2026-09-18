import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { afterEach, describe, expect, it } from 'vitest'
import { DescriptionCallout } from './callout-extension'
import { DescriptionDiagram } from './diagram-extension'
import { DescriptionFile, DescriptionVideo } from './file-extension'
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
})
