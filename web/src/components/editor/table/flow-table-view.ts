import type { Editor } from '@tiptap/core'
import type { Node as ProseMirrorNode } from '@tiptap/pm/model'
import type { EditorView } from '@tiptap/pm/view'
import { TableView } from '@tiptap/extension-table'
import { ReactRenderer } from '@tiptap/react'
import { TableChrome } from './table-chrome'

/**
 * Tiptap's own TableView DOM (the column-resizing plugin finds `table > colgroup` by itself, so that DOM must stay
 * untouched) wrapped in a positioned `.flow-table` that also hosts the React hover chrome next to it. The chrome is
 * rendered through ReactRenderer so it lives inside the EditorContent tree and still sees the i18n / router contexts.
 */
export class FlowTableView extends TableView {
  private readonly renderer: ReactRenderer

  constructor(node: ProseMirrorNode, cellMinWidth: number, view: EditorView, HTMLAttributes: Record<string, unknown>, editor: Editor, getPos: () => number | undefined) {
    super(node, cellMinWidth, view, HTMLAttributes)
    const scroller = this.dom
    const outer = document.createElement('div')
    outer.className = 'flow-table'
    outer.appendChild(scroller)
    this.renderer = new ReactRenderer(TableChrome, { editor, props: { editor, getPos }, className: 'flow-table__chrome-host' })
    this.renderer.element.contentEditable = 'false'
    outer.appendChild(this.renderer.element)
    this.dom = outer
  }

  ignoreMutation(mutation: Parameters<TableView['ignoreMutation']>[0]) {
    const target = mutation.target
    if (target === this.dom || this.renderer.element.contains(target)) return true
    return super.ignoreMutation(mutation)
  }

  stopEvent(event: Event) {
    return event.target instanceof Node && this.renderer.element.contains(event.target)
  }

  destroy() {
    this.renderer.destroy()
  }
}
