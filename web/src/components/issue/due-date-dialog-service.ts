export type DueDateRequest = { id: number; value?: string; resolve: (value: string | null) => void }

let pending: DueDateRequest | undefined
let nextId = 0
const listeners = new Set<() => void>()
const emit = () => listeners.forEach(listener => listener())

/** Opens the "Set due date" dialog; resolves with the chosen ISO date, or null when it is dismissed. */
export function requestDueDate(value?: string) {
  return new Promise<string | null>(resolve => {
    pending?.resolve(null)
    pending = { id: ++nextId, value, resolve }
    emit()
  })
}

export function settleDueDate(result: string | null) {
  const current = pending
  pending = undefined
  current?.resolve(result)
  emit()
}

export const currentDueDateRequest = () => pending
export const subscribeDueDateRequests = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener) } }
