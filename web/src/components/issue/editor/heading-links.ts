import { Fragment, type Node as ProseMirrorNode } from '@tiptap/pm/model'
import type { Editor } from '@tiptap/react'

export const HEADING_SELECTOR = 'h1,h2,h3,h4'

export function headingSlug(text: string) {
  const slug = text.normalize('NFKD').toLocaleLowerCase().replace(/[̀-ͯ]/g, '').replace(/[^\p{Letter}\p{Number}]+/gu, '-').replace(/^-+|-+$/g, '')
  return slug || 'heading'
}

/** Slugs for every H1–H3 in document order; repeated titles get a numeric suffix like GitHub anchors. */
export function headingSlugs(root: HTMLElement) {
  const seen = new Map<string, number>()
  const result = new Map<Element, string>()
  for (const heading of root.querySelectorAll(HEADING_SELECTOR)) {
    const base = headingSlug(heading.textContent ?? '')
    const count = seen.get(base) ?? 0
    seen.set(base, count + 1)
    result.set(heading, count ? `${base}-${count}` : base)
  }
  return result
}

export function headingLink(slug: string, location: Pick<Location, 'origin' | 'pathname' | 'search'> = window.location) {
  return `${location.origin}${location.pathname}${location.search}#${encodeURIComponent(slug)}`
}

/** Scrolls to the heading named by the URL hash. Returns true when a heading matched. */
export function revealHeadingFromHash(root: HTMLElement, hash = window.location.hash) {
  if (!hash || hash.length < 2) return false
  let wanted: string
  try { wanted = decodeURIComponent(hash.slice(1)) } catch { return false }
  for (const [heading, slug] of headingSlugs(root)) {
    if (slug !== wanted) continue
    heading.scrollIntoView?.({ block: 'start' })
    return true
  }
  return false
}

/**
 * Converts the heading at `pos` plus every following sibling up to the next heading of the same or a
 * higher level into an open collapsible section whose summary is the heading text.
 */
export function makeHeadingCollapsible(editor: Editor, pos: number) {
  const { state } = editor
  const heading = state.doc.nodeAt(pos)
  const { details, detailsSummary, detailsContent, paragraph } = state.schema.nodes
  if (!heading || heading.type.name !== 'heading' || !details || !detailsSummary || !detailsContent || !paragraph) return false
  const $pos = state.doc.resolve(pos)
  const parent = $pos.parent
  const level = Number(heading.attrs.level) || 1
  const body: ProseMirrorNode[] = []
  let end = pos + heading.nodeSize
  for (let index = $pos.index() + 1; index < parent.childCount; index++) {
    const child = parent.child(index)
    if (child.type.name === 'heading' && (Number(child.attrs.level) || 1) <= level) break
    body.push(child)
    end += child.nodeSize
  }
  const text: ProseMirrorNode[] = []
  heading.forEach(child => { if (child.isText) text.push(child) })
  let summary: ProseMirrorNode
  try { summary = detailsSummary.create(null, text) }
  catch { summary = detailsSummary.create(null, heading.textContent ? state.schema.text(heading.textContent) : undefined) }
  const content = detailsContent.create(null, body.length ? body : [paragraph.create()])
  const section = details.create({ open: true }, [summary, content])
  if (!parent.canReplace($pos.index(), $pos.index() + 1 + body.length, Fragment.from(section))) return false
  editor.view.dispatch(state.tr.replaceWith(pos, end, section).scrollIntoView())
  editor.view.focus()
  return true
}
