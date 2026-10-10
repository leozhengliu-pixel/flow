import { ChevronsDownUp, EllipsisVertical, Link2 } from 'lucide-react'
import type { Editor } from '@tiptap/react'
import { useEffect, useRef, useState, type RefObject } from 'react'
import { toast } from 'sonner'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { useI18n } from '@/i18n/i18n'
import { HEADING_SELECTOR, headingLink, headingSlug, headingSlugs, makeHeadingCollapsible, revealHeadingFromHash } from './heading-links'

const BUTTON_SIZE = 20
const BUTTON_OFFSET = 28

type Target = { element: HTMLElement; top: number; left: number }

/** Linear-style "Heading actions" affordance: a 20px button 28px left of a hovered H1–H4. */
export function HeadingActions({ editor, rootRef }: { editor: Editor; rootRef: RefObject<HTMLDivElement | null> }) {
  const { t } = useI18n()
  const [target, setTarget] = useState<Target | null>(null)
  const [open, setOpen] = useState(false)
  const openRef = useRef(open)
  openRef.current = open

  useEffect(() => {
    const root = rootRef.current
    if (!root) return
    const position = (element: HTMLElement): Target => {
      const bounds = root.getBoundingClientRect()
      const rect = element.getBoundingClientRect()
      return { element, left: rect.left - bounds.left - BUTTON_OFFSET, top: rect.top - bounds.top + (rect.height - BUTTON_SIZE) / 2 }
    }
    const onMove = (event: MouseEvent) => {
      if (openRef.current) return
      const node = event.target instanceof Element ? event.target : null
      if (node?.closest('.description-heading-actions')) return
      const heading = node?.closest<HTMLElement>(HEADING_SELECTOR)
      if (heading && editor.view.dom.contains(heading) && editor.isEditable) {
        setTarget(current => {
          const next = position(heading)
          return current && current.element === heading && current.top === next.top && current.left === next.left ? current : next
        })
      } else setTarget(current => current ? null : current)
    }
    const onLeave = () => { if (!openRef.current && !editor.isFocused) setTarget(null) }
    // Keyboard reveal: when the caret sits in an H1–H4 the button shows for that heading, so it can be
    // reached with Tab from the editor without a mouse.
    const caretHeading = () => {
      const { $from } = editor.state.selection
      for (let depth = $from.depth; depth > 0; depth--) {
        const node = $from.node(depth)
        if (node.type.name !== 'heading' || (Number(node.attrs.level) || 1) > 4) continue
        const dom = editor.view.nodeDOM($from.before(depth))
        return dom instanceof HTMLElement ? dom : null
      }
      return null
    }
    const onSelection = () => {
      if (openRef.current || !editor.isFocused || !editor.isEditable) return
      const heading = caretHeading()
      if (heading) setTarget(current => current?.element === heading ? current : position(heading))
      else setTarget(current => current ? null : current)
    }
    const onBlur = ({ event }: { event: FocusEvent }) => {
      const next = event.relatedTarget instanceof Element ? event.relatedTarget : null
      if (!openRef.current && !next?.closest('.description-heading-actions')) setTarget(null)
    }
    root.addEventListener('mousemove', onMove)
    root.addEventListener('mouseleave', onLeave)
    editor.on('selectionUpdate', onSelection)
    editor.on('focus', onSelection)
    editor.on('blur', onBlur)
    return () => {
      root.removeEventListener('mousemove', onMove)
      root.removeEventListener('mouseleave', onLeave)
      editor.off('selectionUpdate', onSelection)
      editor.off('focus', onSelection)
      editor.off('blur', onBlur)
    }
  }, [editor, rootRef])

  useEffect(() => {
    let revealed = false
    const reveal = () => { if (!revealed && !editor.isDestroyed) revealed = revealHeadingFromHash(editor.view.dom) }
    const onHash = () => { revealed = false; reveal() }
    reveal()
    // Collaborative content can arrive after mount; keep trying until the anchor resolves once.
    const onUpdate = () => { if (!revealed) reveal() }
    editor.on('update', onUpdate)
    editor.on('transaction', onUpdate)
    window.addEventListener('hashchange', onHash)
    return () => {
      editor.off('update', onUpdate)
      editor.off('transaction', onUpdate)
      window.removeEventListener('hashchange', onHash)
    }
  }, [editor])

  if (!target || !editor.isEditable) return null
  const headingPos = () => {
    try {
      const pos = editor.view.posAtDOM(target.element, 0) - 1
      return editor.state.doc.nodeAt(pos)?.type.name === 'heading' ? pos : -1
    } catch { return -1 }
  }
  const copyLink = () => {
    const slug = headingSlugs(editor.view.dom).get(target.element) ?? headingSlug(target.element.textContent ?? '')
    const url = headingLink(slug)
    void navigator.clipboard?.writeText(url).then(() => toast.success(t('Link copied')), () => toast.error(t('Could not copy link')))
  }
  const collapse = () => {
    const pos = headingPos()
    if (pos >= 0) makeHeadingCollapsible(editor, pos)
    setTarget(null)
  }
  return <DropdownMenu open={open} onOpenChange={next => { setOpen(next); if (!next) setTarget(null) }} modal={false}>
    <DropdownMenuTrigger asChild>
      <button
        type="button"
        className="description-heading-actions"
        aria-label={t('Heading actions')}
        style={{ left: target.left, top: target.top }}
        onMouseDown={event => event.preventDefault()}
        onBlur={event => {
          const next = event.relatedTarget instanceof Node ? event.relatedTarget : null
          if (!openRef.current && !(next && editor.view.dom.contains(next))) setTarget(null)
        }}
      ><EllipsisVertical size={16} aria-hidden/></button>
    </DropdownMenuTrigger>
    <DropdownMenuContent className="description-heading-menu" align="end" sideOffset={1} onCloseAutoFocus={event => event.preventDefault()}>
      <DropdownMenuItem onSelect={copyLink}><Link2 size={16} aria-hidden/>{t('Copy link')}</DropdownMenuItem>
      <DropdownMenuItem onSelect={collapse}><ChevronsDownUp size={16} aria-hidden/>{t('Make collapsible')}</DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
}
