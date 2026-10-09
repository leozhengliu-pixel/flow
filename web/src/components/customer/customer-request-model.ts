import { markdownPlainText } from '@/lib/markdown-plain-text'
import type { Customer, CustomerRequest } from '@/types/flow'
import type { CustomerPick } from './customer-request-events'

/** Linear's `CustomerNeedPriority`: priority 1 marks a request as important. */
export const IMPORTANT_PRIORITY = 1

export function isImportantRequest(request: Pick<CustomerRequest, 'priority'>) {
  return (request.priority ?? 0) >= IMPORTANT_PRIORITY
}

export type CustomerRequestOrdering = 'createdAt' | 'customerName' | 'customerRevenue'
export type CustomerRequestViewPreferences = { ordering: CustomerRequestOrdering; direction: 'asc' | 'desc'; importantFirst: boolean }

export const CUSTOMER_REQUEST_ORDERINGS: { value: CustomerRequestOrdering; label: string }[] = [
  { value: 'createdAt', label: 'Created' },
  { value: 'customerName', label: 'Customer name' },
  { value: 'customerRevenue', label: 'Customer revenue' },
]

/** Linear's default direction per ordering: newest / highest revenue first, names A→Z. */
export function defaultOrderingDirection(ordering: CustomerRequestOrdering): 'asc' | 'desc' {
  return ordering === 'customerName' ? 'asc' : 'desc'
}

export const DEFAULT_CUSTOMER_REQUEST_VIEW: CustomerRequestViewPreferences = { ordering: 'createdAt', direction: 'desc', importantFirst: false }

/** Per-surface display options ("embedded" on issues, "project" on project pages), kept per browser. */
export function readCustomerRequestView(surface: 'issue' | 'project'): CustomerRequestViewPreferences {
  try {
    const stored = JSON.parse(localStorage.getItem(`flow:customer-requests-view:${surface}`) ?? 'null') as Partial<CustomerRequestViewPreferences> | null
    if (!stored) return DEFAULT_CUSTOMER_REQUEST_VIEW
    const ordering = CUSTOMER_REQUEST_ORDERINGS.some(option => option.value === stored.ordering) ? stored.ordering as CustomerRequestOrdering : 'createdAt'
    return { ordering, direction: stored.direction === 'asc' || stored.direction === 'desc' ? stored.direction : defaultOrderingDirection(ordering), importantFirst: Boolean(stored.importantFirst) }
  } catch {
    return DEFAULT_CUSTOMER_REQUEST_VIEW
  }
}

export function writeCustomerRequestView(surface: 'issue' | 'project', view: CustomerRequestViewPreferences) {
  try { localStorage.setItem(`flow:customer-requests-view:${surface}`, JSON.stringify(view)) } catch { /* storage unavailable */ }
}

/**
 * One row per customer (Linear's `groupType: "customer"`): the newest request leads and the
 * customer's other requests on the same issue / project are its additional needs. Requests without
 * a customer each get their own row.
 */
export type CustomerRequestGroup = { key: string; primary: CustomerRequest; additional: CustomerRequest[]; customer?: Customer }

export function groupRequestsByCustomer(requests: CustomerRequest[], customers: ReadonlyMap<string, Customer>): CustomerRequestGroup[] {
  const groups = new Map<string, CustomerRequestGroup>()
  const sorted = [...requests].sort((left, right) => Date.parse(right.createdAt) - Date.parse(left.createdAt))
  for (const request of sorted) {
    const key = request.customerId ? `customer:${request.customerId}` : `request:${request.id}`
    const group = groups.get(key)
    if (group) group.additional.push(request)
    else groups.set(key, { key, primary: request, additional: [], customer: request.customerId ? customers.get(request.customerId) : undefined })
  }
  return [...groups.values()]
}

export function orderRequestGroups(groups: CustomerRequestGroup[], view: CustomerRequestViewPreferences) {
  const sign = view.direction === 'asc' ? 1 : -1
  const value = (group: CustomerRequestGroup) => {
    if (view.ordering === 'customerName') return group.customer?.name.toLocaleLowerCase() ?? '￿'
    if (view.ordering === 'customerRevenue') return group.customer?.annualRevenue ?? 0
    return Date.parse(group.primary.createdAt)
  }
  return [...groups].sort((left, right) => {
    if (view.importantFirst) {
      const important = Number(groupIsImportant(right)) - Number(groupIsImportant(left))
      if (important) return important
    }
    const a = value(left), b = value(right)
    if (typeof a === 'string' && typeof b === 'string') {
      // Requests without a customer sort last in both directions, like Linear's U+FFFF name.
      if (a === '￿' || b === '￿') return a === b ? 0 : a === '￿' ? 1 : -1
      return a.localeCompare(b) * sign
    }
    return ((a as number) - (b as number)) * sign
  })
}

export function groupIsImportant(group: Pick<CustomerRequestGroup, 'primary' | 'additional'>) {
  return isImportantRequest(group.primary) || group.additional.some(isImportantRequest)
}

/** The collapsed row's preview: the request text on one line (Linear's `subtitle(0)`), else the source host. */
export function requestSubtitle(request: Pick<CustomerRequest, 'body' | 'sourceUrl'>) {
  const text = markdownPlainText(request.body)
  if (text) return text
  return request.sourceUrl ? sourceHost(request.sourceUrl) ?? request.sourceUrl : ''
}

/** Text that looks like a link (Linear's `isUrl`): the picker then offers "Add link as source". */
export function looksLikeUrl(value: string) {
  const trimmed = value.trim()
  if (!trimmed || /\s/.test(trimmed)) return false
  try {
    const url = new URL(trimmed.includes('://') ? trimmed : `https://${trimmed}`)
    return /^https?:$/.test(url.protocol) && url.hostname.includes('.') && !url.hostname.startsWith('.') && !url.hostname.endsWith('.')
  } catch {
    return false
  }
}

/** Normalizes a typed source link (adds https://); undefined when it is not a valid URL. */
export function normalizeSourceUrl(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return ''
  const url = trimmed.includes('://') ? trimmed : `https://${trimmed}`
  try {
    const parsed = new URL(url)
    return /^https?:$/.test(parsed.protocol) && parsed.hostname.includes('.') ? url : undefined
  } catch {
    return undefined
  }
}

export function sourceHost(value: string) {
  try { return new URL(value.includes('://') ? value : `https://${value}`).hostname } catch { return undefined }
}

const MINUTE = 60_000, HOUR = 60 * MINUTE, DAY = 24 * HOUR, WEEK = 7 * DAY, MONTH = 30 * DAY, YEAR = 365 * DAY

/** Linear's relative time without suffix for a request row (`now`, `5m`, `3h`, `2d`, `3w`, `4mo`, `1y`). */
export function compactRelativeTime(value: string, now = Date.now()) {
  const diff = Math.max(0, now - Date.parse(value))
  if (diff < MINUTE) return 'now'
  if (diff < HOUR) return `${Math.floor(diff / MINUTE)}m`
  if (diff < DAY) return `${Math.floor(diff / HOUR)}h`
  if (diff < WEEK) return `${Math.floor(diff / DAY)}d`
  if (diff < MONTH) return `${Math.floor(diff / WEEK)}w`
  if (diff < YEAR) return `${Math.floor(diff / MONTH)}mo`
  return `${Math.floor(diff / YEAR)}y`
}

/** Linear's tooltip date (`Wed, Oct 7, 2026, 3:04 PM`). */
export function requestTimestamp(value: string, locale?: string) {
  return new Intl.DateTimeFormat(locale, { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' }).format(new Date(value))
}

/** "Acme, Globex and 2 others" (Linear's project overview customers summary). */
export function customersSummary(names: string[], spellOut = 3) {
  const shown = names.length === spellOut + 1 ? names.length : Math.min(names.length, spellOut)
  const visible = names.slice(0, shown)
  const rest = names.length - shown
  if (rest > 0) return { names: visible, rest }
  return { names: visible, rest: 0 }
}

/**
 * Local overlay for requests written from this tab: the workspace snapshot catches up through the
 * realtime metadata refresh, so created / updated rows show right away and deleted ones disappear.
 */
export type RequestOverlay = { upserts: ReadonlyMap<string, CustomerRequest>; removed: ReadonlySet<string> }
export const EMPTY_OVERLAY: RequestOverlay = { upserts: new Map(), removed: new Set() }

export function applyRequestOverlay(base: CustomerRequest[], overlay: RequestOverlay, belongs: (request: CustomerRequest) => boolean) {
  const byId = new Map(base.map(request => [request.id, request]))
  for (const [id, request] of overlay.upserts) {
    const current = byId.get(id)
    if (!current || Date.parse(current.updatedAt) <= Date.parse(request.updatedAt)) byId.set(id, request)
  }
  return [...byId.values()].filter(request => !overlay.removed.has(request.id) && belongs(request))
}

export type CustomerPickOption = { id: string; label: string; kind: 'unknown' | 'customer' | 'create' | 'source' | 'hint'; customer?: Customer; pick?: CustomerPick }

/**
 * Linear's customer options (`ContextualMenuActions.lB`): "Unknown customer" (when allowed), the
 * customers by name, then for typed text `Create new customer: "…"` — or "Add link as source"
 * when the text is a link. With no customers and no text: "Type a name to create your first customer".
 */
export function customerPickOptions(customers: readonly Customer[], query: string, { withUnknown = true, withSource = true }: { withUnknown?: boolean; withSource?: boolean } = {}): CustomerPickOption[] {
  const text = query.trim()
  const sorted = [...customers].sort((left, right) => left.name.localeCompare(right.name))
  if (!text) {
    if (!sorted.length) return [{ id: 'hint', kind: 'hint', label: 'Type a name to create your first customer' }]
    return [
      ...(withUnknown ? [{ id: '__unknown', kind: 'unknown' as const, label: 'Unknown customer', pick: {} }] : []),
      ...sorted.map(customer => ({ id: customer.id, kind: 'customer' as const, label: customer.name, customer, pick: { customerId: customer.id } })),
    ]
  }
  const needle = text.toLocaleLowerCase()
  const matches = sorted.filter(customer => customer.name.toLocaleLowerCase().includes(needle) || customer.domains?.some(domain => domain.toLocaleLowerCase().includes(needle)))
  const options: CustomerPickOption[] = [
    ...(withUnknown && 'unknown customer'.includes(needle) ? [{ id: '__unknown', kind: 'unknown' as const, label: 'Unknown customer', pick: {} }] : []),
    ...matches.map(customer => ({ id: customer.id, kind: 'customer' as const, label: customer.name, customer, pick: { customerId: customer.id } })),
  ]
  if (looksLikeUrl(text)) {
    const url = normalizeSourceUrl(text)
    if (withSource && url) options.push({ id: '__source', kind: 'source', label: 'Add link as source', pick: { sourceUrl: url } })
  } else {
    options.push({ id: '__create', kind: 'create', label: `Create new customer: "${text}"`, pick: { pendingCustomerName: text } })
  }
  return options
}
