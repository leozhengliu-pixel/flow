/** ProseMirror JSON helpers shared by the read-only document views. */
import { isValidDocument, nodeText, type PMNode } from './document-history-diff'
import { markdownToDoc } from './document-markdown-doc'

export interface DocumentSource {
  content?: string
  contentData?: Record<string, unknown>
}

/** The version's ProseMirror JSON: its `contentData`, else its Markdown projection parsed into the same shape. */
export function documentSourceToDoc(source: DocumentSource): PMNode {
  if (isValidDocument(source.contentData)) return source.contentData
  return markdownToDoc(source.content ?? '')
}

export function isEmptyDocument(doc: PMNode): boolean {
  const blocks = doc.content ?? []
  return blocks.length === 0 || blocks.every(block => block.type === 'paragraph' && !nodeText(block).trim() && !(block.content ?? []).length)
}
