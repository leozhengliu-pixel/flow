import { useRef, useState } from 'react'

// Keep every surface on the latest selection while serializing full-array writes.
export function useLabelSelection(selectedIds: string[]) {
  const [pendingIds, setPendingIds] = useState<string[]>()
  const queue = useRef<Promise<unknown>>(Promise.resolve())
  const revision = useRef(0)
  const save = async <T,>(ids: string[], persist: () => Promise<T>) => {
    const current = ++revision.current
    setPendingIds(ids)
    const request = queue.current.catch(() => undefined).then(persist)
    queue.current = request
    try { return await request }
    finally { if (current === revision.current) setPendingIds(undefined) }
  }
  return { selectedIds: pendingIds ?? selectedIds, save }
}
