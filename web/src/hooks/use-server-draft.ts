import { useState, type SetStateAction } from 'react'

/**
 * A form draft that starts from, and keeps following, a server value until the user changes it. When the server
 * value changes (a cached workspace snapshot replaced by the confirming bootstrap, another member or device saving)
 * an untouched draft takes the new value; a draft the user edited is left alone. The draft is adjusted while
 * rendering, so no frame shows the old value.
 */
export function useServerDraft<T>(server: T): [T, (next: SetStateAction<T>) => void] {
  const [state, setState] = useState({ seen: server, draft: server })
  let draft = state.draft
  if (!Object.is(state.seen, server)) {
    if (Object.is(state.draft, state.seen)) draft = server
    setState({ seen: server, draft })
  }
  return [draft, next => setState(current => ({ ...current, draft: typeof next === 'function' ? (next as (value: T) => T)(current.draft) : next }))]
}
