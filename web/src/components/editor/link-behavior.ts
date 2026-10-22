import { Extension, getMarkRange } from '@tiptap/core'
import type { Node as ProseMirrorNode } from '@tiptap/pm/model'
import { Plugin, PluginKey, TextSelection } from '@tiptap/pm/state'
import { Decoration, DecorationSet, type EditorView } from '@tiptap/pm/view'
import { navigateInApp } from '@/lib/app-navigation'
import { appRoutePath } from '@/lib/route-preload'

const OPENABLE_PROTOCOLS = new Set(['http:', 'https:', 'mailto:'])

/** Only web and mail links open; `javascript:`, `data:` and every other scheme is dead. */
export function openableLinkUrl(href: string | null | undefined, base: string = window.location.href): URL | undefined {
  const value = href?.trim()
  if (!value) return undefined
  let url: URL
  try { url = new URL(value, base) } catch { return undefined }
  return OPENABLE_PROTOCOLS.has(url.protocol) ? url : undefined
}

/** Workspace URLs (issues, projects, documents, ...) go through the router; everything else opens in a new tab. */
export function openLink(url: URL, newTab: boolean) {
  if (url.protocol === 'mailto:') { window.open(url.href, '_self'); return }
  if (!newTab && url.origin === window.location.origin) {
    const path = appRoutePath(url)
    if (path && navigateInApp(path)) return
  }
  window.open(url.href, '_blank', 'noopener,noreferrer')
}

function linkHint(url: URL) {
  return url.protocol === 'mailto:' ? url.pathname : url.hostname
}

function linkAnchor(view: EditorView, target: EventTarget | null) {
  const anchor = target instanceof Element ? target.closest<HTMLAnchorElement>('a[href]') : null
  if (!anchor || !view.dom.contains(anchor)) return null
  // Mention chips, embed cards and other node views own their clicks.
  if (anchor.matches('[contenteditable="false"]')) return null
  const owner = anchor.parentElement?.closest('[data-node-view-wrapper], [contenteditable="false"]')
  if (owner && owner !== view.dom && view.dom.contains(owner)) return null
  return anchor
}

function selectionTouches(anchor: HTMLAnchorElement) {
  const selection = window.getSelection()
  if (!selection || selection.isCollapsed || selection.rangeCount === 0) return false
  return selection.getRangeAt(0).intersectsNode(anchor)
}

function selectLink(view: EditorView, anchor: HTMLAnchorElement) {
  const linkType = view.state.schema.marks.link
  if (!linkType) return
  let pos: number
  try { pos = view.posAtDOM(anchor, 0) } catch { return }
  const doc = view.state.doc
  // One step in: the start of the anchor still resolves to the text before it.
  const range = getMarkRange(doc.resolve(Math.min(pos + 1, doc.content.size)), linkType)
  if (!range) return
  view.dispatch(view.state.tr.setSelection(TextSelection.create(doc, range.from, range.to)))
}

function linkHints(doc: ProseMirrorNode) {
  const decorations: Decoration[] = []
  doc.descendants((node, pos) => {
    if (!node.isText) return
    const mark = node.marks.find(item => item.type.name === 'link')
    const url = mark ? openableLinkUrl(mark.attrs.href as string | undefined) : undefined
    if (url) decorations.push(Decoration.inline(pos, pos + node.nodeSize, { 'data-tooltip-text': linkHint(url) }))
  })
  return DecorationSet.create(doc, decorations)
}

/**
 * Link clicks, in every editor and read-only rich text: a link opens when the text is not being edited
 * (workspace URLs in-app, others in a new tab, ⌘/Ctrl-click always in a new tab); while the caret is in the
 * editor a click selects the whole link instead, so it can be edited, and selecting or double-clicking never
 * opens it. Hovering a link shows its host.
 */
export const LinkBehavior = Extension.create({
  name: 'linkBehavior',
  addProseMirrorPlugins() {
    let pressedWhileEditing = false
    return [
      new Plugin({
        key: new PluginKey('linkBehavior'),
        state: {
          init: (_, state) => linkHints(state.doc),
          apply: (tr, value) => tr.docChanged ? linkHints(tr.doc) : value,
        },
        props: {
          decorations(state) { return this.getState(state) },
          handleDOMEvents: {
            mousedown(view, event) {
              pressedWhileEditing = view.editable && event.button === 0 && view.hasFocus()
              return false
            },
            click(view, event) {
              const anchor = linkAnchor(view, event.target)
              const wasEditing = pressedWhileEditing
              pressedWhileEditing = false
              if (!anchor) return false
              // The editor never follows the anchor itself; the router's document listener must not see the click either.
              event.preventDefault()
              if (event.button !== 0) return true
              const url = openableLinkUrl(anchor.getAttribute('href'))
              if (!url) return true
              if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) { openLink(url, true); return true }
              if (event.detail > 1 || selectionTouches(anchor)) return true
              if (wasEditing) { selectLink(view, anchor); return true }
              openLink(url, false)
              return true
            },
            auxclick(view, event) {
              if (!view.editable || event.button !== 1) return false
              const anchor = linkAnchor(view, event.target)
              if (!anchor) return false
              event.preventDefault()
              const url = openableLinkUrl(anchor.getAttribute('href'))
              if (url) openLink(url, true)
              return true
            },
          },
        },
      }),
    ]
  },
})
