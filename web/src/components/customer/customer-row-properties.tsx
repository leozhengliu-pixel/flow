import type { FeatureSettings } from '@/types/flow'
import { TooltipContent, TooltipProvider, TooltipRoot, TooltipTrigger } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import { CustomerLogoPile } from './customer-logo-pile'
import { CustomerRevenueGlyph, ImportantCustomerGlyph } from './customer-filter-glyphs'
import { displayRevenue, formatCompactRevenue, revenueSuffix } from '@/components/issue-explorer/customer-filter'
import './customer-row-properties.css'

export type RowCustomer = { id: string; name: string; logoUrl?: string; annualRevenue?: number }

export interface CustomerRowData {
  customers: RowCustomer[]
  /** Distinct requesting customers; unknown (deleted) customers count once. */
  customerCount: number
  importantCustomerIds: string[]
  settings?: Partial<FeatureSettings>
  /** Lists use Linear's rounded badges, boards its square ones. */
  variant?: 'rounded' | 'square' | 'plain'
}

/** Linear's sort for the customer list: important first, then revenue, then name. */
function sortedCustomers(customers: RowCustomer[], important: Set<string>) {
  return [...customers].sort((a, b) => Number(important.has(b.id)) - Number(important.has(a.id)) || (b.annualRevenue ?? 0) - (a.annualRevenue ?? 0) || a.name.localeCompare(b.name))
}

function totalRevenue(customers: RowCustomer[]) {
  return customers.reduce((sum, customer) => sum + (customer.annualRevenue ?? 0), 0)
}

/**
 * The Customers display property: `[▲ N |] logo pile name-or-count` in a rounded chip, with Linear's
 * hover summary ("2 customers · 1 important", revenue, customer list).
 */
export function CustomersRowChip({ customers, customerCount, importantCustomerIds, settings, variant = 'rounded' }: CustomerRowData) {
  const { t } = useI18n()
  if (customerCount <= 0) return null
  const important = new Set(importantCustomerIds)
  const sorted = sortedCustomers(customers, important)
  const known = sorted.length
  const importantCount = important.size
  return <TooltipProvider delayDuration={450} skipDelayDuration={300}><TooltipRoot>
    <TooltipTrigger asChild>
      <span className="customer-row-chip" data-variant={variant} data-customer-chip="" aria-label={`${customerCount} ${t(customerCount === 1 ? 'customer' : 'customers')}`}>
        {importantCount > 0 && <><span className="customer-row-chip__important"><ImportantCustomerGlyph size={14}/><span>{importantCount}</span></span><span className="customer-row-chip__divider" aria-hidden="true"/></>}
        <span className="customer-row-chip__customers">
          <CustomerLogoPile customers={sorted} size={16} appendNoCustomer={customerCount !== known && known < 3}/>
          {customerCount === 1 && known === 1 && <span className="customer-row-chip__name" data-i18n-ignore>{sorted[0].name}</span>}
          {(customerCount > 1 || (customerCount === 1 && known === 0)) && <span className="customer-row-chip__count">{customerCount}</span>}
        </span>
      </span>
    </TooltipTrigger>
    <TooltipContent side="top" className="customer-row-popover flow-tooltip-content--card">
      <CustomersSummary customers={sorted} customerCount={customerCount} importantCustomerIds={importantCustomerIds} settings={settings}/>
    </TooltipContent>
  </TooltipRoot></TooltipProvider>
}

function CustomersSummary({ customers, customerCount, importantCustomerIds, settings, hideRevenue = false }: CustomerRowData & { hideRevenue?: boolean }) {
  const { t } = useI18n()
  const important = new Set(importantCustomerIds)
  const revenue = totalRevenue(customers)
  const unknown = customerCount - customers.length
  return <div className="customer-row-popover__body">
    <div className="customer-row-popover__data">
      <div className="customer-row-popover__counts">
        <span>{customerCount} {t(customerCount === 1 ? 'customer' : 'customers')}</span>
        {important.size > 0 && <><span className="customer-row-popover__dot">·</span><span className="customer-row-popover__important">{important.size} {t('important')}</span></>}
      </div>
      {!hideRevenue && revenue > 0 && <RevenueLine revenue={revenue} settings={settings}/>}
    </div>
    <ul className="customer-row-popover__list">
      {customers.slice(0, 5).map(customer => <li key={customer.id}>
        <CustomerLogoPile customers={[customer]} size={16}/>
        <span className="customer-row-popover__name" data-i18n-ignore>{customer.name}</span>
        {important.has(customer.id) && <ImportantCustomerGlyph size={16} className="customer-row-popover__flag"/>}
      </li>)}
      {unknown > 0 && <li><CustomerLogoPile customers={[]} size={16} appendNoCustomer/><span className="customer-row-popover__name">{t('Unknown customer')}</span><span className="customer-row-popover__unknown-count">{unknown}</span></li>}
    </ul>
  </div>
}

function RevenueLine({ revenue, settings }: { revenue: number; settings?: Partial<FeatureSettings> }) {
  const { t } = useI18n()
  return <div className="customer-row-popover__revenue">
    <span className="customer-row-popover__revenue-label">{t(settings?.customerRevenueFormat === 'monthly' ? 'Monthly revenue' : 'Annual revenue')}</span>
    <span>{formatCompactRevenue(displayRevenue(revenue, settings), settings)}</span>
  </div>
}

/**
 * The Customer revenue display property: `$ 1.2M /yr` (DollarBill glyph, compact amount in the
 * workspace currency and revenue unit). Hover lists customers by revenue.
 */
export function CustomerRevenueRowChip({ customers, settings, variant = 'rounded' }: Pick<CustomerRowData, 'customers' | 'settings' | 'variant'>) {
  const { t } = useI18n()
  const revenue = totalRevenue(customers)
  if (revenue <= 0) return null
  const amount = formatCompactRevenue(displayRevenue(revenue, settings), settings)
  const byRevenue = [...customers].filter(customer => (customer.annualRevenue ?? 0) > 0).sort((a, b) => (b.annualRevenue ?? 0) - (a.annualRevenue ?? 0))
  return <TooltipProvider delayDuration={450} skipDelayDuration={300}><TooltipRoot>
    <TooltipTrigger asChild>
      <span className="customer-row-chip" data-variant={variant} data-customer-revenue-chip="" aria-label={`${t('Customer revenue')} ${amount}${revenueSuffix(settings)}`}>
        {variant !== 'plain' && <CustomerRevenueGlyph size={14} className="customer-row-chip__revenue-icon"/>}
        <span className="customer-row-chip__revenue">{amount}{variant !== 'plain' && <span className="customer-row-chip__suffix">{t(revenueSuffix(settings))}</span>}</span>
      </span>
    </TooltipTrigger>
    <TooltipContent side="top" className="customer-row-popover flow-tooltip-content--card">
      <div className="customer-row-popover__body">
        <div className="customer-row-popover__data"><RevenueLine revenue={revenue} settings={settings}/></div>
        <ul className="customer-row-popover__list">
          {byRevenue.slice(0, 5).map(customer => <li key={customer.id}>
            <CustomerLogoPile customers={[customer]} size={16}/>
            <span className="customer-row-popover__name" data-i18n-ignore>{customer.name}</span>
            <span className="customer-row-popover__amount">{formatCompactRevenue(displayRevenue(customer.annualRevenue ?? 0, settings), settings)}</span>
          </li>)}
        </ul>
      </div>
    </TooltipContent>
  </TooltipRoot></TooltipProvider>
}
