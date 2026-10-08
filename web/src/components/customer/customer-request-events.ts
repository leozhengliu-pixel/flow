import type { CustomerRequest, Project } from '@/types/flow'
import { isMacPlatform } from '@/components/project-detail/project-detail-shortcuts'

/**
 * What the "Select customer…" step picked: an existing customer, a name for a customer that is
 * only created when the request is saved (Linear's `deferCreation`), a pasted link to use as the
 * request's source, or no customer ("Unknown customer").
 */
export type CustomerPick = { customerId?: string; pendingCustomerName?: string; sourceUrl?: string }

type PickerRequest = { id: number; title?: string; onPick: (pick: CustomerPick) => void }
let current: PickerRequest | undefined
let sequence = 0
/** Mounted picker hosts; the first one renders the palette. */
export const customerRequestPickerHosts: string[] = []
const listeners = new Set<() => void>()
const emit = () => { for (const listener of listeners) listener() }
export const subscribeCustomerRequestPicker = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener) } }

/**
 * Opens the "Select customer…" palette (Linear's add-customer-request sub-action) on the mounted
 * host. Returns false when no host is mounted on the current page.
 */
export function openCustomerRequestPicker(onPick: (pick: CustomerPick) => void, title?: string) {
  if (!customerRequestPickerHosts.length) return false
  current = { id: ++sequence, title, onPick }
  emit()
  return true
}

export function closeCustomerRequestPicker() {
  current = undefined
  emit()
}

export const currentCustomerRequestPicker = () => current

/** Event other surfaces (the issue "…" menu, ⌘K) dispatch to start a customer request on an issue. */
export const ADD_CUSTOMER_REQUEST_EVENT = 'flow:add-customer-request'
/** Event ⌘K dispatches to start a customer request on the open project. */
export const ADD_PROJECT_CUSTOMER_REQUEST_EVENT = 'flow:add-project-customer-request'

/**
 * Asks the open issue page to start a customer request ("Add customer request…"): it shows the
 * "Select customer…" palette and then its composer. False when the issue is not open on a page.
 */
export function requestIssueCustomerRequest(issueId: string) {
  return !window.dispatchEvent(new CustomEvent(ADD_CUSTOMER_REQUEST_EVENT, { detail: { issueId }, cancelable: true }))
}

export function requestProjectCustomerRequest(projectId: string) {
  return !window.dispatchEvent(new CustomEvent(ADD_PROJECT_CUSTOMER_REQUEST_EVENT, { detail: { projectId }, cancelable: true }))
}

/** Linear's add-customer-request shortcut: ⌃R on macOS, Ctrl+Alt+R elsewhere (also while typing). */
export function isAddCustomerRequestShortcut(event: KeyboardEvent, mac = isMacPlatform()) {
  return event.key.toLowerCase() === 'r' && event.ctrlKey && !event.metaKey && !event.shiftKey && (mac ? !event.altKey : event.altKey)
}

/** Shown the way Linear prints it ("Ctrl R"; Ctrl Alt R off macOS). */
export const ADD_CUSTOMER_REQUEST_SHORTCUT = () => isMacPlatform() ? 'Ctrl R' : 'Ctrl Alt R'

/** The pick made from the overview or ⌘K, waiting for the project's Customers tab to open its composer. */
export type PendingProjectPick = { projectId: string; pick: CustomerPick; key: number }
let pendingPick: PendingProjectPick | undefined
const pendingListeners = new Set<() => void>()
export const pendingProjectPick = () => pendingPick
export function setPendingProjectPick(value: PendingProjectPick | undefined) {
  pendingPick = value
  for (const listener of pendingListeners) listener()
}
export function subscribePendingProjectPick(listener: () => void) {
  pendingListeners.add(listener)
  return () => { pendingListeners.delete(listener) }
}

/** A project's requests: its own and its issues' (Linear's `project.needs`). */
export function projectCustomerRequests(requests: CustomerRequest[], project: Pick<Project, 'id'>, issueIds: ReadonlySet<string>) {
  return requests.filter(request => !request.archivedAt && (request.projectId === project.id || (request.issueId && issueIds.has(request.issueId))))
}
