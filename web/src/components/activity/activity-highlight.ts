import { useEffect, useLayoutEffect, useState, type RefObject } from 'react'

export interface ActivityHighlightTarget {
  kind: 'activity' | 'comment'
  id: string
  /** A different notification can point to the same entry. */
  key: string
}

export function activityTargetFromHash(hash: string): ActivityHighlightTarget | undefined {
  const match = /^#(comment|activity)-(.+)$/.exec(hash)
  if (!match) return undefined
  try {
    return { kind: match[1] as ActivityHighlightTarget['kind'], id: decodeURIComponent(match[2]), key: hash }
  } catch {
    return undefined
  }
}

export function useActivityHighlightTarget(target?: ActivityHighlightTarget) {
  const key = target?.key
  const [anchor, setAnchor] = useState(() => ({ hash: target ? '' : window.location.hash, key, generation: 0 }))
  useEffect(() => {
    const changed = () => setAnchor(current => current.hash === window.location.hash && current.key === key ? current : {hash: window.location.hash, key, generation: current.generation + 1})
    window.addEventListener('hashchange', changed)
    window.addEventListener('popstate', changed)
    return () => { window.removeEventListener('hashchange', changed); window.removeEventListener('popstate', changed) }
  }, [key])
  const hashTarget = anchor.key === key ? activityTargetFromHash(anchor.hash) : undefined
  return {
    target: hashTarget ? {...hashTarget, key: `${hashTarget.key}:${anchor.generation}`} : target,
    highlightAnchor: (hash: string) => setAnchor(current => ({hash, key, generation: current.generation + 1})),
  }
}

export function useActivityHighlight(ref: RefObject<HTMLDivElement | null>, target?: ActivityHighlightTarget, contentKey?: string) {
  const kind = target?.kind
  const id = target?.id
  const key = target?.key
  useLayoutEffect(() => {
    const root = ref.current
    if (!root || !kind || !id) return
    const element = [...root.querySelectorAll<HTMLElement>('[data-activity-anchor]')]
      .find(node => node.dataset.activityAnchor === `${kind}-${id}`)
    if (!element) return

    const reduced = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
    element.dataset.activityHighlight = 'enter'
    const frame = window.requestAnimationFrame(() => element.scrollIntoView?.({ behavior: reduced || kind === 'comment' ? 'instant' : 'smooth', block: 'center' }))
    let exiting = false
    const dismiss = () => {
      if (exiting) return
      exiting = true
      element.dataset.activityHighlight = 'dismiss'
    }
    const timer = window.setTimeout(() => {
      if (exiting) return
      exiting = true
      element.dataset.activityHighlight = 'exit'
    }, kind === 'activity' ? 2000 : 7000)
    document.addEventListener('pointerdown', dismiss)
    return () => {
      window.cancelAnimationFrame(frame)
      window.clearTimeout(timer)
      document.removeEventListener('pointerdown', dismiss)
      delete element.dataset.activityHighlight
    }
  }, [ref, kind, id, key, contentKey])
}
