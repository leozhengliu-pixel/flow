import { Extension } from '@tiptap/core'
import { NodeSelection, Plugin, PluginKey, TextSelection } from '@tiptap/pm/state'
import type { EditorView } from '@tiptap/pm/view'

const trailingKey = new PluginKey('flowTrailingParagraph')

/** Appends an empty paragraph after the final block and puts the caret in it. Returns false when the document already ends in a paragraph. */
export function appendTrailingParagraph(view: EditorView) {
  const { state } = view
  const { doc, schema } = state
  const last = doc.lastChild
  if (!last || last.type === schema.nodes.paragraph || !schema.nodes.paragraph) return false
  const pos = doc.content.size
  const tr = state.tr.insert(pos, schema.nodes.paragraph.create())
  tr.setSelection(TextSelection.create(tr.doc, pos + 1)).scrollIntoView()
  view.dispatch(tr)
  return true
}

/**
 * In collaborative editors Tiptap's TrailingNode must stay off (every reader would append a paragraph to the CRDT), so a
 * document can end in a code block, table, callout or collapsible with nowhere to type after it. This adds the paragraph on
 * the user's own gesture instead: ArrowDown/ArrowRight at the end of the final block, or a click in the space below it.
 */
export const TrailingParagraph = Extension.create({
  name: 'flowTrailingParagraph',
  addProseMirrorPlugins() {
    return [new Plugin({
      key: trailingKey,
      props: {
        handleKeyDown: (view, event) => {
          if ((event.key !== 'ArrowDown' && event.key !== 'ArrowRight') || event.shiftKey || event.metaKey || event.ctrlKey || event.altKey || !view.editable) return false
          const { selection, doc } = view.state
          // A selected final block (image, embed, diagram...) or a caret on the last position of the last textblock.
          const onLastBlock = selection instanceof NodeSelection
            ? selection.to === doc.content.size
            : selection.empty && selection.$head.pos + selection.$head.depth === doc.content.size && view.endOfTextblock(event.key === 'ArrowDown' ? 'down' : 'right')
          if (!onLastBlock || !appendTrailingParagraph(view)) return false
          event.preventDefault()
          return true
        },
        handleClick: (view, _pos, event) => {
          if (!view.editable || event.target !== view.dom) return false
          const lastDom = view.dom.lastElementChild as HTMLElement | null
          if (!lastDom || event.clientY <= lastDom.getBoundingClientRect().bottom) return false
          return appendTrailingParagraph(view)
        },
      },
    })]
  },
})
