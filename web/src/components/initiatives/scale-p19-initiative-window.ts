import { useContext, useEffect, useMemo, useState } from 'react'
import { ApplicationStoreContext } from '@/store/application-store-contexts'
import { scaleP19InitiativeCatalogSupported, scaleP19InitiativesEnabled } from '@/lib/scale-p19-surfaces'
import { syncClientFor } from '@/lib/sync-client'
import type { Initiative } from '@/types/flow'

type Entry = { kind: 'group' | 'initiative'; initiative?: Initiative }

export function scaleP19InitiativeEntries<T extends Entry>(entries: T[], index: number) {
  const result: T[] = []
  let position = 0, group: T | undefined
  for (const entry of entries) {
    if (entry.kind === 'group') { group = entry; continue }
    if (position >= index * 100 && position < (index + 1) * 100) {
      if (group) { result.push(group); group = undefined }
      result.push(entry)
    }
    position++
  }
  return result
}

export function useScaleP19InitiativeWindow<T extends Entry>(entries: T[], query: string) {
  const context = useContext(ApplicationStoreContext)
  const data = context?.data
  const client = syncClientFor(data)
  const enabled = Boolean(client) && scaleP19InitiativesEnabled(data)
  const supported = scaleP19InitiativeCatalogSupported(data ?? {}, data?.sync?.collections)
  const [position, setPosition] = useState({ query, index: 0 })
  const index = position.query === query ? position.index : 0
  const total = entries.filter(entry => entry.kind === 'initiative').length
  const currentIndex = Math.min(index, Math.max(0, Math.ceil(total / 100) - 1))
  const items = useMemo(() => enabled && supported ? scaleP19InitiativeEntries(entries, currentIndex) : entries, [currentIndex, enabled, entries, supported])
  useEffect(() => { if (enabled && !supported) client?.requestReset(true) }, [client, enabled, supported])
  return { enabled: enabled && supported, entries: items, page: { index: currentIndex, items: items.filter(item => item.kind === 'initiative'), total, hasMore: (currentIndex + 1) * 100 < total }, onPage: (index: number) => setPosition({ query, index }) }
}
