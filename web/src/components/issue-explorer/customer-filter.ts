import type { FeatureSettings } from '@/types/flow'

/**
 * Linear's numeric customer filters (Customers › Customer count / Important customer count /
 * Customer revenue / Customer size). A value is `<prefix><number>` and the chip's operator holds the
 * comparison; the server receives `<prefix><comparison>:<number>`.
 */
export type CustomerNumberComparison = 'gte' | 'lte' | 'eq' | 'neq'
export type CustomerNumberField = 'count' | 'importantCount' | 'revenue' | 'size'

export const CUSTOMER_NUMBER_COMPARISONS: { value: CustomerNumberComparison; label: string }[] = [
  { value: 'gte', label: 'greater than or equals' },
  { value: 'lte', label: 'less than or equals' },
  { value: 'eq', label: 'equals' },
  { value: 'neq', label: 'not equals' },
]

export const CUSTOMER_NUMBER_PREFIXES: Record<CustomerNumberField, string> = {
  count: 'customer-count:',
  importantCount: 'customer-important-count:',
  revenue: 'customer-revenue:',
  size: 'customer-size:',
}

export function isCustomerNumberComparison(value: unknown): value is CustomerNumberComparison {
  return value === 'gte' || value === 'lte' || value === 'eq' || value === 'neq'
}

/** `customer-count:3` → { field: 'count', amount: 3 }; legacy bucket values (`customer-count:2+`, `customer-revenue:any`) are not numeric. */
export function parseCustomerNumberValue(value: string): { field: CustomerNumberField; amount: number } | undefined {
  for (const [field, prefix] of Object.entries(CUSTOMER_NUMBER_PREFIXES) as [CustomerNumberField, string][]) {
    if (!value.startsWith(prefix)) continue
    const raw = value.slice(prefix.length)
    if (!/^-?\d+(\.\d+)?$/.test(raw)) return undefined
    return { field, amount: Number(raw) }
  }
  return undefined
}

export function compareCustomerNumber(actual: number, comparison: CustomerNumberComparison, amount: number) {
  if (comparison === 'gte') return actual >= amount
  if (comparison === 'lte') return actual <= amount
  if (comparison === 'eq') return actual === amount
  return actual !== amount
}

/** Linear's number parser: digits with `.`/`,`/`$` stripped, or a `12K` / `1.5M` shorthand. */
export function parseCustomerNumberInput(input: string): number | undefined {
  const trimmed = input.trim()
  const shorthand = trimmed.match(/^(\d+(?:\.\d+)?)\s*(K|M)$/i)
  if (shorthand) return parseFloat(shorthand[1]) * (shorthand[2].toUpperCase() === 'K' ? 1e3 : 1e6)
  const parsed = parseInt(trimmed.replace(/[.,$]/g, ''), 10)
  return Number.isNaN(parsed) ? undefined : parsed
}

function revenueCurrency(settings?: Partial<FeatureSettings>) {
  return /^[A-Z]{3}$/.test(settings?.customerRevenueCurrency ?? '') ? settings!.customerRevenueCurrency! : 'USD'
}

/** Full (non-compact) currency, no decimals: Linear's revenue filter labels. */
export function formatRevenueAmount(amount: number, settings?: Partial<FeatureSettings>, locale?: string) {
  try {
    return new Intl.NumberFormat(locale, { style: 'currency', currency: revenueCurrency(settings), maximumFractionDigits: 0 }).format(amount)
  } catch {
    return String(amount)
  }
}

/** Compact currency for row chips and popovers ("$1.2M"). */
export function formatCompactRevenue(amount: number, settings?: Partial<FeatureSettings>, locale?: string) {
  try {
    return new Intl.NumberFormat(locale, { style: 'currency', currency: revenueCurrency(settings), notation: 'compact', maximumFractionDigits: 1 }).format(amount)
  } catch {
    return String(amount)
  }
}

/** Revenue in the workspace's display unit: monthly settings divide the stored annual amount by 12. */
export function displayRevenue(annual: number, settings?: Partial<FeatureSettings>) {
  return settings?.customerRevenueFormat === 'monthly' ? Math.round(annual / 12) : annual
}

/** Typed revenue (display unit) → stored annual amount. */
export function annualRevenue(typed: number, settings?: Partial<FeatureSettings>) {
  return settings?.customerRevenueFormat === 'monthly' ? typed * 12 : typed
}

/** Linear's revenue unit suffix shown after compact revenue chips. */
export function revenueSuffix(settings?: Partial<FeatureSettings>) {
  return settings?.customerRevenueFormat === 'monthly' ? '/mo' : '/yr'
}

/** The chip value label for a stored numeric value. */
export function customerNumberValueLabel(field: CustomerNumberField, amount: number, settings?: Partial<FeatureSettings>, locale?: string) {
  if (field === 'revenue') return formatRevenueAmount(displayRevenue(amount, settings), settings, locale)
  return new Intl.NumberFormat(locale).format(amount)
}

/** Value sent to the server's customer filter: `customer-count:gte:3`. */
export function customerNumberQueryValue(value: string, comparison: CustomerNumberComparison) {
  const parsed = parseCustomerNumberValue(value)
  if (!parsed) return value
  return `${CUSTOMER_NUMBER_PREFIXES[parsed.field]}${comparison}:${parsed.amount}`
}

type RowDataSource = { customers?: { id: string; name: string; logoUrl?: string; annualRevenue?: number; size?: number; ownerId?: string; status?: string; tier?: string }[]; customerRequests?: { issueId?: string; projectId?: string; customerId: string; priority?: number; archivedAt?: string }[]; workspaceSettings?: { featureSettings?: Partial<FeatureSettings> } }

/** Customers behind a set of (non-archived) requests: the row chips' data plus the fields customer filters match. */
export function customerRowDataFor(matches: (request: NonNullable<RowDataSource['customerRequests']>[number]) => boolean, data: RowDataSource) {
  const byId = new Map((data.customers ?? []).map(customer => [customer.id, customer]))
  const customers = new Map<string, NonNullable<RowDataSource['customers']>[number]>()
  const important = new Set<string>()
  let unknown = false
  for (const request of data.customerRequests ?? []) {
    if (request.archivedAt || !matches(request)) continue
    const customer = byId.get(request.customerId)
    if (!customer) { unknown = true; continue }
    customers.set(customer.id, customer)
    if (request.priority) important.add(customer.id)
  }
  const list = [...customers.values()]
  return {
    customers: list.map(customer => ({ id: customer.id, name: customer.name, logoUrl: customer.logoUrl, annualRevenue: customer.annualRevenue })),
    customerCount: list.length + (unknown ? 1 : 0),
    importantCustomerIds: [...important],
    settings: data.workspaceSettings?.featureSettings,
    hasUnknownCustomer: unknown,
    customerIds: list.map(customer => customer.id),
    customerOwnerIds: list.map(customer => customer.ownerId ?? ''),
    customerStatuses: list.map(customer => customer.status ?? '').filter(Boolean),
    customerTiers: list.map(customer => customer.tier ?? '').filter(Boolean),
    customerRevenues: list.map(customer => customer.annualRevenue ?? 0),
    customerSizes: list.map(customer => customer.size ?? 0),
  }
}

/** Requesting customers of one issue (non-archived requests), for row chips outside the explorer. */
export function issueCustomerRowData(issueId: string, data: RowDataSource) {
  return customerRowDataFor(request => request.issueId === issueId, data)
}

/** A project's customers: requests on the project and on its issues (Linear's project customer count). */
export function projectCustomerRowData(projectId: string, issueIds: ReadonlySet<string>, data: RowDataSource) {
  return customerRowDataFor(request => request.projectId === projectId || Boolean(request.issueId && issueIds.has(request.issueId)), data)
}

/** The row fields customer filters read (issue rows and project items share them). */
export interface CustomerMatchFields {
  customerIds?: string[]
  hasUnknownCustomer?: boolean
  customerCount?: number
  importantCustomerIds?: string[]
  customerOwnerIds?: string[]
  customerStatuses?: string[]
  customerTiers?: string[]
  customerRevenues?: number[]
  customerSizes?: number[]
}

/** Linear: count filters compare the row's counts; revenue / size match when some requesting customer does. */
export function matchesCustomerNumberValues(row: CustomerMatchFields, values: string[], comparison: CustomerNumberComparison) {
  return values.some(value => {
    const parsed = parseCustomerNumberValue(value)
    if (!parsed) return false
    if (parsed.field === 'count') return compareCustomerNumber(row.customerCount ?? ((row.customerIds?.length ?? 0) + (row.hasUnknownCustomer ? 1 : 0)), comparison, parsed.amount)
    if (parsed.field === 'importantCount') return compareCustomerNumber(row.importantCustomerIds?.length ?? 0, comparison, parsed.amount)
    const amounts = parsed.field === 'revenue' ? row.customerRevenues ?? [] : row.customerSizes ?? []
    return amounts.some(amount => compareCustomerNumber(amount, comparison, parsed.amount))
  })
}

/** Customer name / owner / status / tier values, plus the legacy bucket values saved views may still hold. */
export function matchesCustomerValues(row: CustomerMatchFields, values: string[]) {
  const count = row.customerCount ?? ((row.customerIds?.length ?? 0) + (row.hasUnknownCustomer ? 1 : 0))
  return values.some(value => {
    if (value === 'customer:') return Boolean(row.hasUnknownCustomer)
    if (value.startsWith('customer:')) return Boolean(row.customerIds?.includes(value.slice(9)))
    if (value === 'customer-count:0') return count === 0
    if (value === 'customer-count:1') return count === 1
    if (value === 'customer-count:2+') return count >= 2
    if (value === 'customer-owner:') return Boolean(row.customerIds?.length) && !(row.customerOwnerIds ?? []).some(Boolean)
    if (value.startsWith('customer-owner:')) return Boolean(row.customerOwnerIds?.includes(value.slice(15)))
    if (value.startsWith('customer-status:')) return Boolean(row.customerStatuses?.includes(value.slice(16)))
    if (value.startsWith('customer-tier:')) return Boolean(row.customerTiers?.includes(value.slice(14)))
    if (value === 'customer-revenue:') return Boolean(row.customerIds?.length) && !(row.customerRevenues ?? []).some(amount => amount > 0)
    if (value === 'customer-revenue:any') return (row.customerRevenues ?? []).some(amount => amount > 0)
    if (value === 'customer-size:') return Boolean(row.customerIds?.length) && !(row.customerSizes ?? []).some(size => size > 0)
    if (value === 'customer-size:any') return (row.customerSizes ?? []).some(size => size > 0)
    return false
  })
}
