import type { JSONContent } from '@tiptap/core'
import type { Editor } from '@tiptap/react'
import { prosemirrorJSONToYDoc } from 'y-prosemirror'
import { encodeStateAsUpdate, type Doc } from 'yjs'

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

export function serializeDescription(editor: Editor, collaborativeDocument?: Doc): DescriptionSnapshot {
  const document = editor.getJSON()
  const ydoc = collaborativeDocument ?? prosemirrorJSONToYDoc(editor.schema, document, 'prosemirror')
  return {
    markdown: editor.getMarkdown(),
    document,
    documentJSON: JSON.stringify(document),
    contentState: bytesToBase64(encodeStateAsUpdate(ydoc)),
  }
}

export function descriptionDocumentJSON(value: string, state?: string): JSONContent {
  if (state) {
    try { return JSON.parse(state) as JSONContent }
    catch { /* Fall through to the plain-text legacy projection. */ }
  }
  const paragraphs = value.split(/\n{2,}/).map(text => text.replaceAll('\n', ' ').trim()).filter(Boolean)
  return {
    type: 'doc',
    content: paragraphs.length
      ? paragraphs.map(text => ({ type: 'paragraph', content: [{ type: 'text', text }] }))
      : [{ type: 'paragraph' }],
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
