import type { JSONContent } from '@tiptap/core'
import type { Editor } from '@tiptap/react'
import { prosemirrorJSONToYDoc } from 'y-prosemirror'
import { encodeStateAsUpdate } from 'yjs'

export interface DescriptionSnapshot {
  markdown: string
  document: JSONContent
  documentJSON: string
  contentState: string
}

export function parseDescriptionContent(value: string, state?: string): { content: string | JSONContent; contentType: 'markdown' | 'json' } {
  if (state) {
    try {
      return { content: JSON.parse(state) as JSONContent, contentType: 'json' }
    } catch {
      // Older records only have the Markdown projection.
    }
  }
  if (!value.trim()) return { content: { type: 'doc', content: [{ type: 'paragraph' }] }, contentType: 'json' }
  return { content: value, contentType: 'markdown' }
}

export function serializeDescription(editor: Editor): DescriptionSnapshot {
  const document = editor.getJSON()
  const ydoc = prosemirrorJSONToYDoc(editor.schema, document)
  return {
    markdown: editor.getMarkdown(),
    document,
    documentJSON: JSON.stringify(document),
    contentState: bytesToBase64(encodeStateAsUpdate(ydoc)),
  }
}

export function sameDocument(editor: Editor, state?: string) {
  if (!state) return false
  try {
    return JSON.stringify(editor.getJSON()) === JSON.stringify(JSON.parse(state))
  } catch {
    return false
  }
}

function bytesToBase64(bytes: Uint8Array) {
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary)
}
