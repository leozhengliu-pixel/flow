/**
 * "Show author names": grey 12px labels in the left margin at the start of each
 * author run. Authors come from the saved revisions (document-authors.ts); the
 * labels are placed next to the matching top-level blocks of the editor DOM and
 * re-measured when the editor changes. Nothing runs while the toggle is off, and
 * the derivation is debounced and runs when the browser is idle.
 */
import { useEffect, useRef, useState } from 'react'

import type { FlowDocument, User } from '@/types/flow'
import { authorRuns, deriveBlockAuthors } from './document-authors'

interface Label { key: string; name: string; top: number; lineHeight: string }

const DERIVE_DELAY_MS = 450
const MAX_LABEL_BLOCKS = 600

function editorBlocks(root: Element) {
  return Array.from(root.children).filter(element => element.nodeType === 1 && !element.classList.contains('ProseMirror-widget') && !element.classList.contains('ProseMirror-gapcursor') && element.tagName !== 'BR').slice(0, MAX_LABEL_BLOCKS)
}

function parseState(state: string | undefined): Record<string, unknown> | undefined {
  if (!state) return undefined
  try {
    const parsed = JSON.parse(state)
    return parsed && typeof parsed === 'object' && parsed.type === 'doc' ? parsed as Record<string, unknown> : undefined
  } catch { return undefined }
}

const idle = (callback: () => void) => {
  const scheduler = window as Window & { requestIdleCallback?: (callback: () => void, options?: { timeout: number }) => number; cancelIdleCallback?: (handle: number) => void }
  if (scheduler.requestIdleCallback) { const handle = scheduler.requestIdleCallback(callback, { timeout: 1500 }); return () => scheduler.cancelIdleCallback?.(handle) }
  const handle = window.setTimeout(callback, 0)
  return () => window.clearTimeout(handle)
}

export function DocumentAuthorLabels({ document, state, lastEditor, enabled, shell }: {
  document: Pick<FlowDocument, 'creator' | 'revisions'>
  /** The editor's current ProseMirror JSON (string). */
  state: string | undefined
  lastEditor: User
  enabled: boolean
  /** The positioned element wrapping the editor (labels are placed relative to it). */
  shell: HTMLElement | null
}) {
  const [authors, setAuthors] = useState<User[]>([])
  const [labels, setLabels] = useState<Label[]>([])
  const authorsRef = useRef<User[]>([])
  const frame = useRef(0)

  // Derive per-block authors (debounced, idle) whenever the content or revisions change.
  useEffect(() => {
    if (!enabled) { setAuthors([]); setLabels([]); return }
    let cancelIdle: (() => void) | undefined
    const timer = window.setTimeout(() => {
      cancelIdle = idle(() => {
        const json = parseState(state)
        if (json) { setAuthors(deriveBlockAuthors(document, json, lastEditor)); return }
        // No block data (a document that only has Markdown): one run owned by the creator.
        setAuthors([document.creator])
      })
    }, DERIVE_DELAY_MS)
    return () => { window.clearTimeout(timer); cancelIdle?.() }
  }, [enabled, state, document, lastEditor])

  // Place a label at the start of each author run, and keep it in place as the editor changes.
  useEffect(() => {
    authorsRef.current = authors
    if (!enabled || !shell || !authors.length) { setLabels([]); return }
    const root = shell.querySelector('.flow-prosemirror, .ProseMirror')
    if (!root) { setLabels([]); return }
    const measure = () => {
      frame.current = 0
      const blocks = editorBlocks(root)
      if (!blocks.length) { setLabels([]); return }
      const known = authorsRef.current
      const origin = shell.getBoundingClientRect().top
      // The label for a single-run document (or one without block data) sits at the first block.
      const runs = known.length === 1 ? [{ index: 0, author: known[0] }] : authorRuns(known)
      const next: Label[] = []
      for (const run of runs) {
        const block = blocks[run.index]
        if (!block) continue
        next.push({ key: `${run.index}:${run.author.id}`, name: run.author.displayName || run.author.name, top: Math.round(block.getBoundingClientRect().top - origin), lineHeight: getComputedStyle(block).lineHeight })
      }
      setLabels(previous => previous.length === next.length && previous.every((label, index) => label.key === next[index].key && label.top === next[index].top) ? previous : next)
    }
    const schedule = () => { if (!frame.current) frame.current = requestAnimationFrame(measure) }
    measure()
    const observer = new MutationObserver(schedule)
    observer.observe(root, { childList: true })
    const resize = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(schedule)
    resize?.observe(root)
    return () => { observer.disconnect(); resize?.disconnect(); if (frame.current) cancelAnimationFrame(frame.current); frame.current = 0 }
  }, [authors, enabled, shell])

  if (!enabled) return null
  return <>{labels.map(label => <span className="document-author-name" data-i18n-ignore key={label.key} style={{ top: label.top, lineHeight: label.lineHeight }}>{label.name}</span>)}</>
}
