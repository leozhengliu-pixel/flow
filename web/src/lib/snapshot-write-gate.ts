/**
 * While a workspace is painted from a local snapshot that no bootstrap has confirmed yet, writes for that
 * workspace wait here. Once the fresh bootstrap is applied they are sent as usual; if the cached view is dropped
 * instead (access lost, a failed load, another viewer, another workspace) they fail like a request that never
 * reached the server, so the caller's usual error handling runs and nothing is sent against data the server
 * never confirmed. Reads, sign-in/sign-out and other workspaces are never held.
 */
export class SnapshotUnconfirmedError extends TypeError {
  constructor() {
    super('The workspace could not be loaded, so the change was not sent.')
    this.name = 'SnapshotUnconfirmedError'
  }
}

type Held = { workspaceKey: string; promise: Promise<void>; resolve: () => void; reject: (error: Error) => void }

let held: Held | undefined

export function holdSnapshotWrites(workspaceKey: string): void {
  if (!workspaceKey || held?.workspaceKey === workspaceKey) return
  rejectSnapshotWrites()
  let resolve!: () => void, reject!: (error: Error) => void
  const promise = new Promise<void>((done, fail) => { resolve = done; reject = fail })
  // A held write that the caller abandons must not surface as an unhandled rejection.
  promise.catch(() => undefined)
  held = { workspaceKey, promise, resolve, reject }
}

/** The held workspace was confirmed by a fresh bootstrap: send its writes. */
export function releaseSnapshotWrites(workspaceKey?: string): void {
  if (!held || workspaceKey && held.workspaceKey !== workspaceKey) return
  const current = held
  held = undefined
  current.resolve()
}

/** The cached view was dropped without a confirmation: fail its writes. */
export function rejectSnapshotWrites(workspaceKey?: string): void {
  if (!held || workspaceKey && held.workspaceKey !== workspaceKey) return
  const current = held
  held = undefined
  current.reject(new SnapshotUnconfirmedError())
}

export function heldSnapshotWorkspace(): string | undefined {
  return held?.workspaceKey
}

/** The wait for a request, or undefined when it may go out now. */
export function snapshotWriteGate(method: string | undefined, url: string, workspaceKey: string | null | undefined): Promise<void> | undefined {
  if (!held) return undefined
  const verb = (method ?? 'GET').toUpperCase()
  if (verb === 'GET' || verb === 'HEAD' || verb === 'OPTIONS') return undefined
  let path = url
  try { path = new URL(url, 'http://local').pathname } catch { /* keep the raw value */ }
  if (path.startsWith('/api/auth/')) return undefined
  return workspaceKey === held.workspaceKey ? held.promise : undefined
}
