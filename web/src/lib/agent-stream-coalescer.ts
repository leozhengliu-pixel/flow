import type { AgentStreamEvent } from './agent-stream'

/** Batch token deltas to animation frames while keeping lifecycle events immediate. */
export function createAgentStreamEventCoalescer(onEvent: (event: AgentStreamEvent) => void) {
  let pending: AgentStreamEvent[] = []
  let frame: number | ReturnType<typeof setTimeout> | undefined
  const schedule = (callback: () => void) => typeof globalThis.requestAnimationFrame === 'function'
    ? globalThis.requestAnimationFrame(callback)
    : globalThis.setTimeout(callback, 16)
  const cancel = (handle: number | ReturnType<typeof setTimeout>) => {
    if (typeof globalThis.cancelAnimationFrame === 'function' && typeof handle === 'number') globalThis.cancelAnimationFrame(handle)
    else globalThis.clearTimeout(handle)
  }
  const flush = () => {
    if (frame !== undefined) { cancel(frame); frame = undefined }
    const events = pending
    pending = []
    events.forEach(onEvent)
  }
  const push = (event: AgentStreamEvent) => {
    if (event.type !== 'text.delta') { flush(); onEvent(event); return }
    pending.push(event)
    if (frame === undefined) frame = schedule(flush)
  }
  return { push, flush, cancel: () => { pending = []; if (frame !== undefined) { cancel(frame); frame = undefined } } }
}
