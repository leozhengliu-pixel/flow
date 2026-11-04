import { useCallback, useEffect, useRef } from 'react'
import { useBeforeUnload, useBlocker } from 'react-router-dom'
import { useLatestRef } from './use-latest-ref'

/** Keep router links and native Back/Forward behind the editor's discard dialog. */
export function useUnsavedNavigation(dirty: boolean, confirm: () => Promise<boolean>) {
  const allowed = useRef(false)
  const pending = useRef(false)
  const ask = useLatestRef(confirm)
  const blocker = useBlocker(useCallback(() => dirty && !allowed.current, [dirty]))
  useEffect(() => {
    if (blocker.state !== 'blocked' || pending.current) return
    pending.current = true
    void ask.current().then(discard => {
      if (discard) blocker.proceed()
      else blocker.reset()
    }).finally(() => { pending.current = false })
  }, [blocker, ask])
  useBeforeUnload(useCallback(event => {
    if (!dirty || allowed.current) return
    event.preventDefault()
    event.returnValue = ''
  }, [dirty]))
  return () => { allowed.current = true }
}
