import { useEffect, useLayoutEffect, useRef, useState } from 'react'
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
export function useIdentifierColumnWidth(identifiers: Iterable<string>) {
  const probeRef = useRef<HTMLSpanElement>(null)
  const samples = identifierSamples(identifiers)
  const signature = samples.join('\n')
  const [width, setWidth] = useState<{ signature: string; px: number } | undefined>()
  const [fontEpoch, setFontEpoch] = useState(0)
  // Web fonts that finish loading after the first measure change the text widths.
  useEffect(() => {
    const fonts = typeof document === 'undefined' ? undefined : document.fonts
    if (!fonts) return
    const remeasure = () => setFontEpoch(epoch => epoch + 1)
    void fonts.ready.then(remeasure)
    fonts.addEventListener?.('loadingdone', remeasure)
    return () => fonts.removeEventListener?.('loadingdone', remeasure)
  }, [])
  useLayoutEffect(() => {
    const probe = probeRef.current
    if (!probe || !samples.length) return
    let widest = 0
    for (const sample of samples) {
      probe.textContent = sample
      widest = Math.max(widest, Math.floor(probe.getBoundingClientRect().width))
    }
    probe.textContent = ''
    // Nothing measurable (hidden list, no layout): keep the content-sized column.
    if (widest <= 0) return
    setWidth(current => current?.signature === signature && current.px === widest ? current : { signature, px: widest })
    // `samples` is derived from `signature`.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signature, fontEpoch])
  return { probe: <span ref={probeRef} aria-hidden className={styles.identifierProbe}/>, width: width?.signature === signature ? width.px : undefined }
}
