import { Node } from '@tiptap/core'
import { Plugin, PluginKey, TextSelection, type EditorState } from '@tiptap/pm/state'
import type { EditorView } from '@tiptap/pm/view'
import { ReactNodeViewRenderer } from '@tiptap/react'
import { EmbedView } from './embed-node-view'
import { translateToChinese } from '@/i18n/i18n'
import { embedSourceForUrl, type EmbedSource } from './embed-providers'
import styles from './embed.module.css'

/** `active` is the popover row the keyboard (or the pointer) is on: 0 embeds, 1 keeps the link. */
type PendingEmbed = { from: number; src: string; source: EmbedSource; active: 0 | 1 }

const pasteKey = new PluginKey<PendingEmbed | null>('embedPaste')

type Translate = (source: string) => string
const defaultTranslate: Translate = source => typeof document !== 'undefined' && document.documentElement.dataset.locale === 'zh-CN' ? translateToChinese(source) : source
let translate: Translate = defaultTranslate

/** Sets the translate function for the paste popover (plain DOM, outside React). It follows the document locale when never set. */
export function setEmbedTranslator(next: Translate | undefined) {
  translate = next ?? defaultTranslate
}

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

/** The paragraph a pasted link was put in, while its popover is showing; null once anything else happened. */
function pendingIn(state: EditorState, pending: PendingEmbed | null | undefined) {
  const node = pending ? state.doc.nodeAt(pending.from) : null
  return pending && node?.type.name === 'paragraph' && node.textContent === pending.src ? { pending, end: pending.from + node.nodeSize } : undefined
}

const SVG_NS = 'http://www.w3.org/2000/svg'
type Shape = [tag: string, attributes: Record<string, string>]
// Lucide's panel-bottom (Embed) and link (Keep as link), drawn as plain DOM because the popover lives outside React.
const EMBED_ICON: Shape[] = [['rect', { width: '18', height: '18', x: '3', y: '3', rx: '2' }], ['path', { d: 'M3 15h18' }]]
const LINK_ICON: Shape[] = [['path', { d: 'M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71' }], ['path', { d: 'M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71' }]]

function iconDom(shapes: Shape[]) {
  const svg = document.createElementNS(SVG_NS, 'svg')
  for (const [name, value] of Object.entries({ width: '16', height: '16', viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': '1.75', 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true' })) svg.setAttribute(name, value)
  for (const [tag, attributes] of shapes) {
    const node = document.createElementNS(SVG_NS, tag)
    for (const [name, value] of Object.entries(attributes)) node.setAttribute(name, value)
    svg.append(node)
  }
  return svg
}

/** The popover sits above the caret when there is room for it, else beside it, else below (measured on Linear). */
const POPOVER_GAP = 4
const POPOVER_MARGIN = 8
const POPOVER_ROOM_ABOVE = 340
const POPOVER_COLUMN_PADDING = 16

/** Places a fixed popover next to the caret: top-end when 340px fit above it, else right-start while it fits in the editor column, else bottom-end. */
function placePopover(view: EditorView, popover: HTMLElement) {
  let caret: { left: number; right: number; top: number; bottom: number }
  try {
    caret = view.coordsAtPos(view.state.selection.head)
  } catch {
    caret = view.dom.getBoundingClientRect()
  }
  const { offsetWidth: width, offsetHeight: height } = popover
  const column = view.dom.getBoundingClientRect()
  let placement = 'top-end'
  let left = caret.left - width
  let top = caret.top - POPOVER_GAP - height
  if (caret.top - POPOVER_GAP - POPOVER_MARGIN < POPOVER_ROOM_ABOVE) {
    if (caret.right + POPOVER_GAP + width <= (column.width ? column.right : window.innerWidth) - POPOVER_COLUMN_PADDING) {
      placement = 'right-start'
      left = caret.right + POPOVER_GAP
      top = caret.top
    } else {
      placement = 'bottom-end'
      top = caret.bottom + POPOVER_GAP
    }
  }
  left = Math.max(POPOVER_MARGIN, Math.min(left, window.innerWidth - POPOVER_MARGIN - width))
  popover.dataset.placement = placement
  popover.style.transform = `translate3d(${Math.round(left * 10) / 10}px, ${Math.round(top * 10) / 10}px, 0)`
}

/** The popover (plain DOM in the document body): Embed (Tab) and Keep as link (Esc), keyboard- and pointer-navigable like a menu. */
function popoverDom(view: EditorView, label: string) {
  const root = document.createElement('div')
  root.className = styles.popover
  root.dataset.embedHint = ''
  root.dataset.i18nIgnore = ''
  const add = (key: 'embed' | 'keep', index: 0 | 1, icon: Shape[], text: string, shortcut: string, run: () => void) => {
    const button = document.createElement('button')
    button.type = 'button'
    button.className = styles.popoverItem
    button.dataset.embedHintAction = key
    const iconBox = document.createElement('span')
    iconBox.className = styles.popoverIcon
    iconBox.append(iconDom(icon))
    const name = document.createElement('span')
    name.className = styles.popoverLabel
    name.textContent = text
    const spacer = document.createElement('span')
    spacer.className = styles.popoverSpacer
    const kbd = document.createElement('kbd')
    kbd.className = styles.popoverKey
    kbd.textContent = shortcut
    button.append(iconBox, name, spacer, kbd)
    // mousedown would move the editor's focus and selection, which dismisses the popover before the click lands.
    button.addEventListener('mousedown', event => event.preventDefault())
    button.addEventListener('mouseover', () => setActive(view, index))
    button.addEventListener('click', run)
    root.append(button)
  }
  add('embed', 0, EMBED_ICON, translate(label), translate('Tab'), () => { acceptEmbed(view); view.focus() })
  add('keep', 1, LINK_ICON, translate('Keep as link'), translate('Esc'), () => { dismissEmbed(view); view.focus() })
  return root
}

function setActive(view: EditorView, active: 0 | 1) {
  const pending = pasteKey.getState(view.state)
  if (pending && pending.active !== active) view.dispatch(view.state.tr.setMeta(pasteKey, { ...pending, active }))
}

/** Tab: the link paragraph becomes the embed, with an empty paragraph after it to carry on typing. */
function acceptEmbed(view: EditorView) {
  const found = pendingIn(view.state, pasteKey.getState(view.state))
  const { schema } = view.state
  if (!found || !schema.nodes.embed) return false
  const { pending, end } = found
  const node = schema.nodes.embed.create({ src: pending.src, embedUrl: pending.source.embedUrl, provider: pending.source.provider })
  const tr = view.state.tr.replaceWith(pending.from, end, [node, schema.nodes.paragraph.create()])
  tr.setSelection(TextSelection.create(tr.doc, pending.from + node.nodeSize + 1)).setMeta(pasteKey, null).scrollIntoView()
  view.dispatch(tr)
  return true
}

/** Esc (or anything else the author does): the popover goes away and the link stays. */
function dismissEmbed(view: EditorView) {
  if (!pasteKey.getState(view.state)) return false
  view.dispatch(view.state.tr.setMeta(pasteKey, null))
  return true
}

/** Fades the popover out (it stops being the hint and its rows stop being actions right away) and removes it. */
function retire(popover: HTMLElement) {
  delete popover.dataset.embedHint
  popover.querySelectorAll<HTMLElement>('[data-embed-hint-action]').forEach(row => { delete row.dataset.embedHintAction })
  popover.dataset.open = 'false'
  window.setTimeout(() => popover.remove(), 140)
}

/**
 * Pasting one embeddable link (YouTube / Loom / Descript / Tella video, X post, Figma, Miro, GitHub file) into an empty
 * paragraph inserts it as a link and opens Linear's popover beside it: "Embed ... Tab / Keep as link Esc". Tab (or Enter on the
 * highlighted row, or a click) turns the paragraph into the embed; Esc, typing, clicking away or leaving the editor keeps the
 * link. Up / Down move the highlight. Add it only to editors that also carry `EmbedNode`.
 */
export const EmbedPasteNode = EmbedNode.extend({
  addProseMirrorPlugins() {
    return [new Plugin<PendingEmbed | null>({
      key: pasteKey,
      state: {
        init: () => null,
        apply: (transaction, value, _old, next) => {
          const meta = transaction.getMeta(pasteKey) as PendingEmbed | null | undefined
          if (meta !== undefined) return meta
          if (!value) return null
          if (transaction.docChanged) return null
          const found = pendingIn(next, value)
          return found && next.selection.from > found.pending.from && next.selection.to < found.end ? value : null
        },
      },
      view: editorView => {
        let popover: HTMLElement | null = null
        let identity = ''
        const reposition = () => { if (popover) placePopover(editorView, popover) }
        const close = () => {
          if (popover) retire(popover)
          popover = null
          identity = ''
          window.removeEventListener('scroll', reposition, true)
          window.removeEventListener('resize', reposition)
        }
        const sync = (view: EditorView) => {
          const found = pendingIn(view.state, pasteKey.getState(view.state))
          if (!found) {
            close()
            return
          }
          const { label = '' } = found.pending.source
          const next = `${found.pending.from}|${found.pending.src}|${label}|${translate('Tab')}`
          if (!popover || identity !== next) {
            if (popover) retire(popover)
            popover = popoverDom(view, label)
            identity = next
            document.body.append(popover)
            window.addEventListener('scroll', reposition, true)
            window.addEventListener('resize', reposition)
            const shown = popover
            window.setTimeout(() => { if (popover === shown) shown.dataset.open = 'true' }, 0)
          }
          popover.querySelectorAll<HTMLElement>('[data-embed-hint-action]').forEach((row, index) => { row.dataset.active = String(index === found.pending.active) })
          placePopover(view, popover)
        }
        sync(editorView)
        return { update: view => sync(view), destroy: () => { if (popover) popover.remove(); popover = null; window.removeEventListener('scroll', reposition, true); window.removeEventListener('resize', reposition) } }
      },
      props: {
        handlePaste: (view, event) => {
          const text = event.clipboardData?.getData('text/plain')?.trim() ?? ''
          const source = text && !/\s/.test(text) ? embedSourceForUrl(text) : undefined
          const { selection, schema } = view.state
          if (!source || !view.editable || !selection.empty || !schema.nodes.embed || !schema.marks.link) return false
          const { $from } = selection
          if ($from.parent.type.name !== 'paragraph' || $from.parent.content.size > 0) return false
          const link = schema.text(text, [schema.marks.link.create({ href: text })])
          const tr = view.state.tr.insert($from.pos, link)
          tr.setSelection(TextSelection.create(tr.doc, $from.pos + text.length)).setMeta(pasteKey, { from: $from.before(), src: text, source, active: 0 } satisfies PendingEmbed).scrollIntoView()
          view.dispatch(tr)
          event.preventDefault()
          return true
        },
        handleKeyDown: (view, event) => {
          const pending = pasteKey.getState(view.state)
          if (!pending || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return false
          let handled = false
          if (event.key === 'Tab') handled = acceptEmbed(view)
          else if (event.key === 'Escape') handled = dismissEmbed(view)
          else if (event.key === 'Enter') handled = pending.active === 0 ? acceptEmbed(view) : dismissEmbed(view)
          else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
            setActive(view, event.key === 'ArrowDown' ? 1 : 0)
            handled = true
          }
          if (handled) event.preventDefault()
          return handled
        },
        handleDOMEvents: {
          // Clicking away (or tabbing out of the editor) keeps the link.
          blur: view => {
            dismissEmbed(view)
            return false
          },
        },
      },
    })]
  },
})
