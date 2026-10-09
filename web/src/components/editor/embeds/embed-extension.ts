import { Node } from '@tiptap/core'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import { ReactNodeViewRenderer } from '@tiptap/react'
import { EmbedView } from './embed-node-view'
import { embedSourceForUrl } from './embed-providers'

const pasteKey = new PluginKey<{ pos: number } | null>('embedPaste')

/** The schema for an embedded player (a block atom): saved with the document, so every editor that can show one needs it. */
export const EmbedSchema = Node.create({
  name: 'embed',
  group: 'block',
  atom: true,
  selectable: true,
  draggable: true,
  addAttributes() {
    return { src: { default: '' }, embedUrl: { default: '' }, provider: { default: '' } }
  },
  parseHTML() {
    return [{ tag: 'div[data-flow-embed]', getAttrs: node => node instanceof HTMLElement ? { src: node.getAttribute('data-src') ?? '', embedUrl: node.getAttribute('data-embed-url') ?? '', provider: node.getAttribute('data-provider') ?? '' } : false }]
  },
  renderHTML({ node }) {
    return ['div', { 'data-flow-embed': '', 'data-src': node.attrs.src, 'data-embed-url': node.attrs.embedUrl, 'data-provider': node.attrs.provider }, ['a', { href: node.attrs.src }, node.attrs.src]]
  },
  renderText({ node }) {
    return String(node.attrs.src)
  },
  renderMarkdown: node => `${String(node.attrs?.src ?? '')}\n`,
})

/** The embed with its player view, for the React editors (the headless schema editor uses EmbedSchema). */
export const EmbedNode = EmbedSchema.extend({
  addNodeView() {
    return ReactNodeViewRenderer(EmbedView)
  },
})

/**
 * Pasting one YouTube / Loom / Descript link into an empty paragraph shows the player; Esc straight afterwards (or the
 * "Keep as link" button) keeps the plain link, as in Linear. Add it only to editors that also carry `EmbedNode`.
 */
export const EmbedPasteNode = EmbedNode.extend({
  addProseMirrorPlugins() {
    return [new Plugin({
      key: pasteKey,
      state: {
        init: () => null,
        apply: (transaction, value) => transaction.getMeta(pasteKey) ?? (transaction.docChanged ? null : value),
      },
      props: {
        handlePaste: (view, event) => {
          const text = event.clipboardData?.getData('text/plain')?.trim() ?? ''
          const source = text && !/\s/.test(text) ? embedSourceForUrl(text) : undefined
          const { selection, schema } = view.state
          if (!source || !view.editable || !selection.empty || !schema.nodes.embed) return false
          const { $from } = selection
          if ($from.parent.type.name !== 'paragraph' || $from.parent.content.size > 0) return false
          const from = $from.before()
          const node = schema.nodes.embed.create({ src: text, embedUrl: source.embedUrl, provider: source.provider })
          const tr = view.state.tr.replaceWith(from, $from.after(), [node, schema.nodes.paragraph.create()])
          tr.setMeta(pasteKey, { pos: from }).scrollIntoView()
          view.dispatch(tr)
          event.preventDefault()
          return true
        },
        handleKeyDown: (view, event) => {
          const pasted = pasteKey.getState(view.state)
          if (event.key !== 'Escape' || !pasted) return false
          const node = view.state.doc.nodeAt(pasted.pos)
          if (node?.type.name !== 'embed') return false
          const { schema } = view.state
          const link = schema.text(String(node.attrs.src), [schema.marks.link.create({ href: node.attrs.src })])
          view.dispatch(view.state.tr.replaceWith(pasted.pos, pasted.pos + node.nodeSize, schema.nodes.paragraph.create(null, link)).setMeta(pasteKey, null))
          return true
        },
      },
    })]
  },
})
