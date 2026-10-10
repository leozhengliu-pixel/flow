import type { Editor } from '@tiptap/react'
import { headingSlug, headingSlugs } from '@/components/issue/editor/heading-links'

export type OutlineHeading = { level: number; text: string; pos: number; slug: string }

const MAX_LEVEL = 4

export function headingElement(editor: Editor, pos: number): HTMLElement | null {
  try {
    const dom = editor.view.nodeDOM(pos)
    return dom instanceof HTMLElement ? dom : null
  } catch { return null }
}

/** H1–H4 of the document in order, with the same anchor slugs used by heading links. */
export function collectHeadings(editor: Editor | null): OutlineHeading[] {
  if (!editor || editor.isDestroyed) return []
  const found: Omit<OutlineHeading, 'slug'>[] = []
  editor.state.doc.descendants((node, pos) => {
    if (node.type.name !== 'heading') return
    const level = Number(node.attrs.level) || 1
    if (level > MAX_LEVEL) return
    found.push({ level, text: node.textContent, pos })
  })
  let domSlugs: Map<Element, string> | null = null
  try { domSlugs = headingSlugs(editor.view.dom) } catch { domSlugs = null }
  const seen = new Map<string, number>()
  return found.map(item => {
    const element = domSlugs ? headingElement(editor, item.pos) : null
    let slug = element ? domSlugs?.get(element) : undefined
    if (!slug) {
      const base = headingSlug(item.text)
      const count = seen.get(base) ?? 0
      seen.set(base, count + 1)
      slug = count ? `${base}-${count}` : base
    }
    return { ...item, slug }
  })
}
