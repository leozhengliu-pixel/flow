import type { Editor } from '@tiptap/react'
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type RefObject } from 'react'
import { headingSlug, headingSlugs } from '@/components/issue/editor/heading-actions'
import { useI18n } from '@/i18n/i18n'
import './outline.css'

export type OutlineHeading = { level: number; text: string; pos: number; slug: string }

const MAX_LEVEL = 4
/** Distance from the scroll container's top edge at which a heading counts as "current". */
const ACTIVE_OFFSET = 120

function headingElement(editor: Editor, pos: number): HTMLElement | null {
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

function sameHeadings(a: OutlineHeading[], b: OutlineHeading[]) {
  return a.length === b.length && a.every((item, index) => {
    const other = b[index]
    return item.level === other.level && item.text === other.text && item.pos === other.pos && item.slug === other.slug
  })
}

function scrollParent(start: HTMLElement | null): HTMLElement | null {
  for (let node = start?.parentElement ?? null; node; node = node.parentElement) {
    const overflow = getComputedStyle(node).overflowY
    if (overflow === 'auto' || overflow === 'scroll' || overflow === 'overlay') return node
  }
  return null
}

/** Floating document outline: dash markers in the left gutter that expand into heading titles on hover. */
export function OutlineMinimap({ editor, rootRef }: { editor: Editor | null; rootRef: RefObject<HTMLElement | null> }) {
  const { t } = useI18n()
  const [headings, setHeadings] = useState<OutlineHeading[]>(() => collectHeadings(editor))
  const [activeSlug, setActiveSlug] = useState<string | null>(null)
  const [placement, setPlacement] = useState<{ left: number; top: number; maxHeight: number } | null>(null)
  const headingsRef = useRef(headings)
  headingsRef.current = headings

  useEffect(() => {
    if (!editor || editor.isDestroyed) { setHeadings(current => current.length ? [] : current); return }
    const run = () => {
      if (editor.isDestroyed) return
      const next = collectHeadings(editor)
      setHeadings(current => sameHeadings(current, next) ? current : next)
    }
    // Typing fires an update per keystroke (and pasted blocks many in a row): collapse a burst into one pass per tick.
    let queued = false
    const refresh = () => {
      if (queued) return
      queued = true
      queueMicrotask(() => { queued = false; run() })
    }
    run()
    editor.on('create', refresh)
    editor.on('update', refresh)
    return () => { editor.off('create', refresh); editor.off('update', refresh) }
  }, [editor])

  const computeActive = useCallback(() => {
    if (!editor || editor.isDestroyed) return
    const list = headingsRef.current
    if (!list.length) { setActiveSlug(null); return }
    const scroller = scrollParent(rootRef.current)
    const base = scroller ? scroller.getBoundingClientRect().top : 0
    let active = list[0].slug
    for (const item of list) {
      const element = headingElement(editor, item.pos)
      if (!element) continue
      if (element.getBoundingClientRect().top - base <= ACTIVE_OFFSET) active = item.slug
      else break
    }
    setActiveSlug(active)
  }, [editor, rootRef])

  useEffect(() => {
    if (!editor || !headings.length) return
    let frame = 0
    const schedule = () => {
      if (typeof requestAnimationFrame !== 'function') { computeActive(); return }
      if (frame) return
      frame = requestAnimationFrame(() => { frame = 0; computeActive() })
    }
    const scroller = scrollParent(rootRef.current)
    const target: HTMLElement | Window = scroller ?? window
    target.addEventListener('scroll', schedule, { passive: true })
    window.addEventListener('resize', schedule)
    // Where available, headings crossing the viewport also trigger a recompute (e.g. layout shifts
    // that do not scroll); jsdom and old browsers fall back to the scroll listener alone.
    let observer: IntersectionObserver | null = null
    if (typeof IntersectionObserver === 'function') {
      try {
        observer = new IntersectionObserver(schedule, { root: scroller, rootMargin: `-${ACTIVE_OFFSET}px 0px 0px 0px` })
        for (const item of headings) {
          const element = headingElement(editor, item.pos)
          if (element) observer.observe(element)
        }
      } catch { observer = null }
    }
    computeActive()
    return () => {
      if (frame && typeof cancelAnimationFrame === 'function') cancelAnimationFrame(frame)
      target.removeEventListener('scroll', schedule)
      window.removeEventListener('resize', schedule)
      observer?.disconnect()
    }
  }, [editor, headings, rootRef, computeActive])

  // Anchor to the scroll area's left edge and centre vertically in its visible rect (clipped to the window).
  const hasHeadings = headings.length > 0
  useEffect(() => {
    if (!hasHeadings) return
    const scroller = scrollParent(rootRef.current)
    const place = () => {
      const rect = scroller?.getBoundingClientRect()
      const top = Math.max(rect?.top ?? 0, 0)
      const bottom = Math.min(rect?.bottom ?? window.innerHeight, window.innerHeight)
      const next = { left: Math.round((rect?.left ?? 0) + 6), top: Math.round((top + bottom) / 2), maxHeight: Math.max(Math.round(bottom - top - 80), 80) }
      setPlacement(current => current && current.left === next.left && current.top === next.top && current.maxHeight === next.maxHeight ? current : next)
    }
    place()
    window.addEventListener('resize', place)
    window.addEventListener('scroll', place, { passive: true })
    let observer: ResizeObserver | null = null
    if (typeof ResizeObserver === 'function') {
      observer = new ResizeObserver(place)
      if (scroller) observer.observe(scroller)
      if (document.body) observer.observe(document.body)
    }
    return () => {
      window.removeEventListener('resize', place)
      window.removeEventListener('scroll', place)
      observer?.disconnect()
    }
  }, [hasHeadings, rootRef])

  const activeIndex = useMemo(() => headings.findIndex(item => item.slug === activeSlug), [headings, activeSlug])

  if (!editor || !headings.length) return null

  const go = (item: OutlineHeading) => {
    const element = headingElement(editor, item.pos) ?? [...headingSlugs(editor.view.dom)].find(([, slug]) => slug === item.slug)?.[0]
    element?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
    setActiveSlug(item.slug)
  }

  return <nav
    className="doc-outline"
    aria-label={t('Document outline')}
    style={placement ? { '--outline-left': `${placement.left}px`, '--outline-top': `${placement.top}px`, '--outline-max-height': `${placement.maxHeight}px` } as CSSProperties : undefined}
  >
    <div className="doc-outline__card">
      {headings.map((item, index) => <button
        key={`${item.pos}-${item.slug}`}
        type="button"
        className={`doc-outline__row doc-outline__row--l${item.level}${index === activeIndex ? ' is-active' : ''}`}
        aria-current={index === activeIndex ? 'location' : undefined}
        title={item.text}
        onMouseDown={event => event.preventDefault()}
        onClick={() => go(item)}
      >
        <span className="doc-outline__marker" aria-hidden/>
        <span className="doc-outline__title">{item.text || t('Untitled heading')}</span>
      </button>)}
    </div>
  </nav>
}
