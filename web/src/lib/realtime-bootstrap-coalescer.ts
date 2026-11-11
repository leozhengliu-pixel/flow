/**
 * Realtime events that change workspace metadata make every open tab reload
 * the metadata bootstrap. Without coordination a burst of events meant one
 * full reload per event per tab, all started on the same tick across every
 * client of the workspace — a self-made load spike on the API for each change.
 *
 * Per workspace this keeps at most one reload running and at most one queued
 * behind it: every event that arrives while a reload runs shares the single
 * reload that starts after the running one finishes, so it still sees the
 * change that event announced. The first reload of a burst starts after a
 * short random delay so tabs spread out instead of arriving together.
 */
type Slot<T> = { running?: Promise<T>; queued?: Promise<T> }

export function createRealtimeCoalescer<T>(load: (workspaceKey: string) => Promise<T>, spreadMs = 1000, random: () => number = Math.random) {
  const slots = new Map<string, Slot<T>>()
  const wait = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms))

  function start(workspaceKey: string, slot: Slot<T>, delay: number): Promise<T> {
    const running: Promise<T> = (delay > 0 ? wait(delay) : Promise.resolve()).then(() => load(workspaceKey)).finally(() => {
      if (slot.running === running) slot.running = undefined
      if (!slot.running && !slot.queued) slots.delete(workspaceKey)
    })
    slot.running = running
    return running
  }

  return function coalesced(workspaceKey: string): Promise<T> {
    let slot = slots.get(workspaceKey)
    if (!slot) {
      slot = {}
      slots.set(workspaceKey, slot)
    }
    if (slot.queued) return slot.queued
    if (slot.running) {
      const current = slot
      const queued = current.running!.catch(() => undefined).then(() => {
        current.queued = undefined
        return start(workspaceKey, current, 0)
      })
      current.queued = queued
      return queued
    }
    return start(workspaceKey, slot, Math.floor(random() * spreadMs))
  }
}
