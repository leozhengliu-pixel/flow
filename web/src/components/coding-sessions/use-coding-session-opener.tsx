import { useState } from 'react'
import { useInRouterContext } from 'react-router-dom'
import type { BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { CodingSessionDialog } from './coding-session-card'
import { codingSessionHref } from './coding-session-page-model'

/**
 * How an entry point opens a session: its PR's review page with the agent panel (else the session page) when the app
 * router is mounted, otherwise (isolated previews,
 * old callers) the dialog. Returns the row props and the dialog element to render.
 */
export function useCodingSessionOpener(session: CodingSession | undefined, data: BootstrapData, onDismiss?: () => void) {
  const routed = useInRouterContext()
  const [open, setOpen] = useState(false)
  if (!session) return { rowProps: {}, dialog: null }
  if (routed) return { rowProps: { href: codingSessionHref(session, data) }, dialog: null }
  return {
    rowProps: { onOpen: () => setOpen(true) },
    dialog: open ? <CodingSessionDialog sessionId={session.id} initial={session} data={data} open onOpenChange={setOpen} onDismiss={onDismiss}/> : null,
  }
}
