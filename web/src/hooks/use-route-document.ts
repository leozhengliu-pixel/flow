import { useEffect, useState, type Dispatch, type SetStateAction } from 'react'
import { getDocument } from '@/lib/api'
import type { BootstrapData } from '@/types/flow'

export type RouteDocumentState = 'loading' | 'missing' | undefined

/**
 * A document link can name a document the loaded workspace data does not hold yet: one an Agent reply just wrote, or
 * one another person or tab created a moment ago. The page then fetches it by its slug (or id) and adds it to the
 * workspace data instead of showing nothing; only a document the server does not know (or the viewer may not open)
 * reads as not found.
 */
export function useRouteDocument({ data, slugId, found, setData }: {
  data: BootstrapData | null | undefined
  /** The document route's slug, or undefined on any other route. */
  slugId: string | undefined
  /** The page already has the document (or its trash entry). */
  found: boolean
  setData: Dispatch<SetStateAction<BootstrapData | null>>
}): RouteDocumentState {
  const workspaceKey = data?.workspace.urlKey
  const wanted = slugId && workspaceKey && data && !data.resourceDetailsOmitted && !found ? `${workspaceKey}\n${slugId}` : undefined
  const [result, setResult] = useState<{ key: string; state: 'missing' }>()
  useEffect(() => {
    if (!wanted) return
    const [workspace, slug] = wanted.split('\n')
    let active = true
    getDocument(slug).then(document => {
      if (!active) return
      // A deleted document is opened from the trash, which the workspace data lists; one this tab has not seen is not found.
      if (document.deletedAt) { setResult({ key: wanted, state: 'missing' }); return }
      setData(current => current && current.workspace.urlKey === workspace && !current.documents.some(item => item.id === document.id)
        ? { ...current, documents: [document, ...current.documents] }
        : current)
    }, () => {
      if (active) setResult({ key: wanted, state: 'missing' })
    })
    return () => { active = false }
  }, [wanted, setData])
  if (!wanted) return undefined
  return result?.key === wanted ? result.state : 'loading'
}
