import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import styles from './my-issues-list.module.css'

/** One identifier per team key and digit count: tabular digits make every number of the same length equally wide. */
export function identifierSamples(identifiers: Iterable<string>): string[] {
  const samples = new Map<string, string>()
  for (const identifier of identifiers) samples.set(identifier.replace(/\d/g, '0'), identifier)
  return [...samples.values()].sort()
}

/**
 * Width of the list-wide identifier column in px, or undefined until measured. The column fits the widest
 * identifier measured without the row's letter-spacing, cut to whole pixels. Render the returned `probe` anywhere
 * in the list: it is an invisible span that carries the identifier typography.
 */
export function useIdentifierColumnWidth(identifiers: Iterable<string>, listId?: string) {
  const probeRef = useRef<HTMLSpanElement>(null)
  const samples = useMemo(() => identifierSamples(identifiers), [identifiers])
  const signature = samples.join('\n')
  const [width, setWidth] = useState<{ signature: string; px: number } | undefined>()
  const [fontEpoch, setFontEpoch] = useState(0)
  const observeWidth = listId !== undefined && typeof ResizeObserver === 'function'
  // Web fonts that finish loading after the first measure change the text widths.
  useEffect(() => {
    if (observeWidth) return
    const fonts = typeof document === 'undefined' ? undefined : document.fonts
    if (!fonts) return
    const remeasure = () => setFontEpoch(epoch => epoch + 1)
    if (fonts.status === 'loading' && typeof fonts.addEventListener !== 'function') void fonts.ready.then(remeasure)
    fonts.addEventListener?.('loadingdone', remeasure)
    return () => fonts.removeEventListener?.('loadingdone', remeasure)
  }, [observeWidth])
  useLayoutEffect(() => {
    const probe = probeRef.current
    if (!observeWidth || !probe || !listId) return
    // Let the browser lay out the probe with the list, avoiding a forced
    // layout while the windowed list is still mounting.
    const observer = new ResizeObserver(([entry]) => {
      const width = Math.floor(entry.contentRect.width)
      document.getElementById(listId)?.style.setProperty('--issue-identifier-width', width > 0 ? `${width}px` : 'max-content')
    })
    observer.observe(probe)
    return () => observer.disconnect()
  }, [observeWidth, listId])
  useLayoutEffect(() => {
    const probe = probeRef.current
    if (observeWidth || !probe || !samples.length) return
    const nodes = samples.map(sample => {
      const node = document.createElement('span')
      node.textContent = sample
      return node
    })
    probe.replaceChildren(...nodes)
    let widest = 0
    // Read every width together to avoid a layout per sample.
    for (const sample of nodes) {
      widest = Math.max(widest, Math.floor(sample.getBoundingClientRect().width))
    }
    probe.replaceChildren()
    if (listId) {
      // The windowed list only needs a CSS value. Keep the measurement from
      // scheduling another React render before its first frame.
      document.getElementById(listId)?.style.setProperty('--issue-identifier-width', widest > 0 ? `${widest}px` : 'max-content')
      return
    }
    // Nothing measurable (hidden list, no layout): keep the content-sized column.
    if (widest <= 0) return
    setWidth(current => current?.signature === signature && current.px === widest ? current : { signature, px: widest })
    // `samples` is derived from `signature`.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signature, fontEpoch, listId, observeWidth])
  const probe = <span ref={probeRef} aria-hidden className={styles.identifierProbe} data-observe-width={observeWidth || undefined}>
    {observeWidth && samples.map(sample => <span key={sample} data-identifier-sample={sample}/>)}
  </span>
  return { probe, width: width?.signature === signature ? width.px : undefined }
}
