import type { CustomerStatus, CustomerTier, FeatureSettings } from '@/types/flow'
import { jsonRequest, request } from '@/lib/api-client'

export function customerRevenueLabel(settings?: Partial<FeatureSettings>) { return settings?.customerRevenueFormat==='monthly'?'Monthly revenue':'Annual revenue' }
export function formatCustomerRevenue(value:number|undefined,settings?:Partial<FeatureSettings>,locale?:string) {
  if(value==null)return '—'
  const amount=settings?.customerRevenueFormat==='monthly'?value/12:value
  const currency=/^[A-Z]{3}$/.test(settings?.customerRevenueCurrency??'')?settings!.customerRevenueCurrency!:'USD'
  return new Intl.NumberFormat(locale,{style:'currency',currency,notation:'compact',maximumFractionDigits:1}).format(amount)
}

/** Settings › Customer requests › Display options. Monthly comes first, as in the reference app. */
export const CUSTOMER_REVENUE_FORMATS = [{ value: 'monthly', label: 'Monthly' }, { value: 'annual', label: 'Annual' }] as const

/** Supported revenue currencies (kept in sync with the API's customerRevenueCurrencies). */
export const CUSTOMER_REVENUE_CURRENCIES = ['AUD', 'BRL', 'CAD', 'CHF', 'CNY', 'DKK', 'EUR', 'GBP', 'HKD', 'INR', 'JPY', 'KRW', 'MXN', 'NOK', 'NZD', 'SEK', 'SGD', 'TWD', 'USD', 'ZAR'] as const
const PREFERRED_CURRENCIES = ['USD', 'EUR', 'GBP']

/** The narrow symbol a currency is displayed with ("$" for USD, "€" for EUR). */
export function currencySymbol(code: string) {
  try {
    return new Intl.NumberFormat('en-US', { style: 'currency', currency: code, currencyDisplay: 'narrowSymbol' }).formatToParts(0).find(part => part.type === 'currency')?.value ?? '$'
  } catch {
    return '$'
  }
}

/** Currency options labelled "USD ($)": USD, EUR and GBP first, then a divider and the rest. */
export function customerCurrencyOptions() {
  const option = (value: string): { value: string; label: string; divider?: boolean } => ({ value, label: `${value} (${currencySymbol(value)})` })
  const preferred = PREFERRED_CURRENCIES.filter(code => (CUSTOMER_REVENUE_CURRENCIES as readonly string[]).includes(code)).map(option)
  const rest = CUSTOMER_REVENUE_CURRENCIES.filter(code => !PREFERRED_CURRENCIES.includes(code)).map((code, index) => ({ ...option(code), divider: index === 0 && preferred.length > 0 }))
  return [...preferred, ...rest]
}

export type CustomerTaxonomyKind = 'status' | 'tier'
export const CUSTOMER_TAXONOMY_MAX_NAME_LENGTH = 25
export const CUSTOMER_TAXONOMY_MAX_DESCRIPTION_LENGTH = 255
/** Colours new statuses and tiers start with. */
export const DEFAULT_NEW_CUSTOMER_STATUS_COLOR = '#95a2b3'
export const DEFAULT_NEW_CUSTOMER_TIER_COLOR = '#8a8f98'

export type CustomerTaxonomyItem = CustomerStatus | CustomerTier
export type CustomerTaxonomyInput = { name?: string; description?: string; color?: string; position?: number }
export type CustomerTaxonomyMutation = CustomerTaxonomyItem & { reassignedCustomerIds?: string[] }

const taxonomyPath = (kind: CustomerTaxonomyKind) => kind === 'tier' ? '/api/customer-tiers' : '/api/customer-statuses'

export function createCustomerTaxonomyItem(kind: CustomerTaxonomyKind, input: CustomerTaxonomyInput) {
  return request<CustomerTaxonomyItem>(taxonomyPath(kind), jsonRequest('POST', input))
}
export function updateCustomerTaxonomyItem(kind: CustomerTaxonomyKind, id: string, input: CustomerTaxonomyInput) {
  return request<CustomerTaxonomyMutation>(`${taxonomyPath(kind)}/${encodeURIComponent(id)}`, jsonRequest('PATCH', input))
}
/** Deletes (archives) a status or tier; the response lists the customers that were moved off it. */
export function archiveCustomerTaxonomyItem(kind: CustomerTaxonomyKind, id: string) {
  return request<CustomerTaxonomyMutation>(`${taxonomyPath(kind)}/${encodeURIComponent(id)}`, jsonRequest('PATCH', { archived: true }))
}
/** Undoes archiveCustomerTaxonomyItem, moving the reassigned customers back. */
export function restoreCustomerTaxonomyItem(kind: CustomerTaxonomyKind, id: string, customerIds: string[] = []) {
  return request<CustomerTaxonomyMutation>(`${taxonomyPath(kind)}/${encodeURIComponent(id)}`, jsonRequest('PATCH', { archived: false, restoreCustomerIds: customerIds }))
}

/** Validates an excluded or generic domain/email entry; returns the error message or undefined. */
export function customerSourceError(value: string, listed: readonly string[]) {
  const source = value.trim()
  if (!source) return 'Source is required'
  if (listed.some(item => item.toLowerCase() === source.toLowerCase())) return 'Source already is already hidden'
  if (source.includes('@') && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(source)) return 'Email address is not valid'
  if (!source.includes('.')) return 'Please enter a valid domain'
  return undefined
}

/** Strips the API's "invalid input: " prefix from a mutation error. */
export function customerSettingsErrorMessage(error: unknown) {
  const text = error instanceof Error ? error.message : 'Could not update customer settings'
  return text.replace(/^invalid input:\s*/i, '')
}
