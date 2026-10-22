import { Extension } from '@tiptap/core'
import { Fragment, Slice, type Mark, type Node as ProseMirrorNode } from '@tiptap/pm/model'
import { Plugin, PluginKey } from '@tiptap/pm/state'

/**
 * Rich-text clipboard marker. What the editor copies starts with an empty `<span data-flow-doc="<document or issue id>">`
 * (the reference app does the same with a `data-doc-id` span), so a paste can tell Flow's own clipboard from foreign html
 * and, for Flow's, which document it came from. Foreign html (a web page, another app) carries no marker.
 */
export const CLIPBOARD_DOC_ATTRIBUTE = 'data-flow-doc'

const MARKER = new RegExp(`${CLIPBOARD_DOC_ATTRIBUTE}="([^"]*)"`, 'i')
const MARKER_SPAN = new RegExp(`^(?:\\s*<meta [^>]*>)*\\s*<span ${CLIPBOARD_DOC_ATTRIBUTE}=`, 'i')

const escapeAttribute = (value: string) => value.replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;')
const unescapeAttribute = (value: string) => value.replace(/&lt;/g, '<').replace(/&quot;/g, '"').replace(/&amp;/g, '&')

/** `html` with the leading marker span for `documentId` (an unknown document marks the clipboard as Flow's only). */
export function withClipboardMarker(html: string, documentId = '') {
  if (!html || MARKER_SPAN.test(html)) return html
  return `<span ${CLIPBOARD_DOC_ATTRIBUTE}="${escapeAttribute(documentId)}"></span>${html}`
}

/** The document id a Flow clipboard was copied from (`''` when it had none), or null for html that is not Flow's. */
export function clipboardOrigin(html: string): string | null {
  if (!html) return null
  const match = MARKER.exec(html.slice(0, 2000))
  return match ? unescapeAttribute(match[1]) : null
}

/** True for html Flow wrote itself: it is already normalised and must not be reshaped like a spreadsheet or web page. */
export const isFlowClipboardHtml = (html: string) => clipboardOrigin(html) !== null

type Options = {
  /** The id of the document or issue the editor shows; marks copies and tells a paste whether it came from the same one. */
  getDocumentId: () => string | undefined
  /** Names of marks that only make sense in the document they were created in (comment anchors). */
  documentBoundMarks: string[]
}

export const clipboardOriginKey = new PluginKey('flowClipboardOrigin')

/** The document id the editor marks its copies with, or null when the editor has no `ClipboardOrigin` extension. */
export function editorClipboardDocumentId(editor: { extensionManager: { extensions: Array<{ name: string; options: unknown }> } }): string | null {
  const extension = editor.extensionManager.extensions.find(item => item.name === 'clipboardOrigin')
  return extension ? ((extension.options as Options).getDocumentId() ?? '') : null
}

function withoutMarks(slice: Slice, names: Set<string>): Slice {
  let changed = false
  const strip = (marks: readonly Mark[]) => {
    const kept = marks.filter(mark => !names.has(mark.type.name))
    if (kept.length !== marks.length) changed = true
    return kept
  }
  const visit = (node: ProseMirrorNode): ProseMirrorNode => {
    if (node.isText) return node.mark(strip(node.marks))
    const children: ProseMirrorNode[] = []
    node.forEach(child => children.push(visit(child)))
    return node.copy(Fragment.from(children))
  }
  const content: ProseMirrorNode[] = []
  slice.content.forEach(node => content.push(visit(node)))
  return changed ? new Slice(Fragment.from(content), slice.openStart, slice.openEnd) : slice
}

/**
 * Copy and cut put the marker on the clipboard html. A paste of Flow's own clipboard from another document drops the marks
 * that point into the source document (comment anchors would reference threads that do not exist here); a paste from
 * the same document keeps them, so cut and paste moves an anchored passage. Foreign html is left to the other paste handlers.
 */
export const ClipboardOrigin = Extension.create<Options>({
  name: 'clipboardOrigin',
  addOptions() {
    return { getDocumentId: () => undefined, documentBoundMarks: ['inlineComment'] }
  },
  addProseMirrorPlugins() {
    const { getDocumentId, documentBoundMarks } = this.options
    let origin: string | null = null
    return [new Plugin({
      key: clipboardOriginKey,
      view: view => {
        // Registered after ProseMirror's own copy/cut handler on the same element, so the html it wrote is complete.
        const mark = (event: ClipboardEvent) => {
          const data = event.clipboardData
          if (!data) return
          try {
            const html = data.getData('text/html')
            if (html) data.setData('text/html', withClipboardMarker(html, getDocumentId() ?? ''))
          } catch { /* a clipboard that cannot be read back stays unmarked */ }
        }
        view.dom.addEventListener('copy', mark)
        view.dom.addEventListener('cut', mark)
        return { destroy: () => { view.dom.removeEventListener('copy', mark); view.dom.removeEventListener('cut', mark) } }
      },
      props: {
        transformPastedHTML: html => {
          origin = clipboardOrigin(html)
          return html
        },
        transformPasted: slice => {
          const source = origin
          origin = null
          if (source !== null && source === (getDocumentId() ?? '')) return slice
          return withoutMarks(slice, new Set(documentBoundMarks))
        },
      },
    })]
  },
})
