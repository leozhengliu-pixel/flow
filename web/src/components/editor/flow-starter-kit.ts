import { StarterKit } from '@tiptap/starter-kit'

/**
 * Nesting order of inline marks, outermost first: link, code, italic, strike, underline, bold. ProseMirror serialises (copy html,
 * rendered DOM, markdown) nested marks in schema order, and the schema orders marks by extension priority. Text that is bold and
 * italic therefore reads `<em><strong>…</strong></em>`, like the reference app. Link keeps its own higher priority (1000).
 */
const MARK_PRIORITY: Record<string, number> = { code: 110, italic: 109, strike: 108, underline: 107, bold: 106 }

/** StarterKit whose inline marks nest in the shared Flow order. Use it in every editor that shares the document schema. */
export const FlowStarterKit = StarterKit.extend({
  addExtensions() {
    return (this.parent?.() ?? []).map(extension => {
      const priority = MARK_PRIORITY[extension.name]
      if (priority === undefined) return extension
      // Like the reference app, inline code keeps its text in an inner span: the chip (padding, ring, radius) is the
      // <code> element and the span carries only the text. Copy and markdown output still use the plain <code>.
      if (extension.name === 'code') {
        return extension.extend({
          priority,
          addMarkView() {
            return ({ HTMLAttributes }: { HTMLAttributes: Record<string, unknown> }) => {
              const dom = document.createElement('code')
              const contentDOM = document.createElement('span')
              for (const [name, value] of Object.entries(HTMLAttributes)) if (value != null && value !== false) dom.setAttribute(name, String(value))
              dom.append(contentDOM)
              return { dom, contentDOM }
            }
          },
        })
      }
      return extension.extend({ priority })
    })
  },
})
