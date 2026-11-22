import type { BootstrapProjection } from '@/lib/api'
import { ApiError } from '@/lib/api-client'
import { readRememberedWorkspace } from '@/lib/navigation-cache'
import type { AccountBootstrap, AuthSession, BootstrapData } from '@/types/flow'


export interface BootstrapRequest {
  key: string
  promise: Promise<BootstrapData>
  controller: AbortController
  /** Started before account confirmed membership; a 401/403/404 is retried normally. */
  speculative?: boolean
  release: () => void
}

export interface StartupDeps {
  fetchSession: () => Promise<AuthSession>
  fetchAccount: () => Promise<AccountBootstrap>
  fetchBootstrap: (workspaceKey: string, projection: BootstrapProjection | undefined, signal: AbortSignal, quiet?: boolean) => Promise<BootstrapData>
}

export interface StartupInput {
  /** Workspace in the URL, or '' on the root route. */
  requestedWorkspaceKey: string
  isRoot: boolean
  projection?: BootstrapProjection
}

export const bootstrapRequestKey = (viewerId: string, workspaceKey: string, projection?: string) => `${viewerId}:${workspaceKey}:${projection ?? 'full'}`

/** Wait before retry `attempt` (from 0) while the server is unreachable: 1 s, doubling, at most 15 s. */
export function startupRetryDelay(attempt: number): number {
  return Math.min(15_000, 1000 * 2 ** Math.max(0, attempt))
}

export function createBootstrapRequest(key: string, start: (signal: AbortSignal) => Promise<BootstrapData>, speculative = false): BootstrapRequest {
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), 30000)
  return { key, controller, speculative, promise: start(controller.signal), release: () => clearTimeout(timeout) }
}

/**
 * Session and account load together. When the viewer has opened this workspace
 * on this device before, its bootstrap starts alongside them and is only kept
 * once account confirms the same viewer is still a member.
 */
export function startStartup(deps: StartupDeps, input: StartupInput) {
  const remembered = readRememberedWorkspace()
  const workspaceKey = input.isRoot ? remembered?.workspaceKey : input.requestedWorkspaceKey === remembered?.workspaceKey ? remembered.workspaceKey : undefined
  const projection = input.isRoot ? undefined : input.projection
  let speculative: BootstrapRequest | undefined
  if (remembered && workspaceKey) {
    speculative = createBootstrapRequest(
      bootstrapRequestKey(remembered.viewerId, workspaceKey, projection),
      signal => deps.fetchBootstrap(workspaceKey, projection, signal, true),
      true,
    )
    speculative.promise.catch(() => {})
  }
  const discard = () => { speculative?.controller.abort(); speculative?.release() }
  const ready = Promise.all([deps.fetchSession(), deps.fetchAccount()]).then(([session, account]) => {
    if (speculative && (account.viewer.id !== remembered?.viewerId || !account.workspaces.some(item => item.workspace.urlKey === workspaceKey))) {
      discard()
      speculative = undefined
    }
    return { session, account }
  }, error => { discard(); throw error })
  return { speculative, ready }
}

let early: { input: StartupInput; startup: ReturnType<typeof startStartup> } | undefined

/** Starts the session, account and remembered-workspace requests before the first render (main.tsx). */
export function beginStartup(deps: StartupDeps, input: StartupInput) {
  early = { input, startup: startStartup(deps, input) }
}

/** The startup begun before the first render when it was begun for the same page; otherwise a new one. */
export function takeStartup(deps: StartupDeps, input: StartupInput) {
  const begun = early
  early = undefined
  if (begun && begun.input.requestedWorkspaceKey === input.requestedWorkspaceKey && begun.input.isRoot === input.isRoot && begun.input.projection === input.projection) return begun.startup
  if (begun) {
    begun.startup.speculative?.controller.abort()
    begun.startup.speculative?.release()
    begun.startup.ready.catch(() => undefined)
  }
  return startStartup(deps, input)
}

/** Reuse a speculative request's answer, repeating it as a normal request if it was refused. */
export function settleBootstrapRequest(request: BootstrapRequest, retry: (signal: AbortSignal) => Promise<BootstrapData>): Promise<BootstrapData> {
  if (!request.speculative) return request.promise
  return request.promise.catch(error => {
    if (error instanceof ApiError && [401, 403, 404].includes(error.status)) return retry(request.controller.signal)
    throw error
  })
}
