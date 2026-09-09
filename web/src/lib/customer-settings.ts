import type { FeatureSettings } from '@/types/flow'

export function customerRevenueLabel(settings?: Partial<FeatureSettings>) { return settings?.customerRevenueFormat==='monthly'?'Monthly revenue':'Annual revenue' }
export function formatCustomerRevenue(value:number|undefined,settings?:Partial<FeatureSettings>,locale?:string) {
  if(value==null)return '—'
  const amount=settings?.customerRevenueFormat==='monthly'?value/12:value
  const currency=/^[A-Z]{3}$/.test(settings?.customerRevenueCurrency??'')?settings!.customerRevenueCurrency!:'USD'
  return new Intl.NumberFormat(locale,{style:'currency',currency,notation:'compact',maximumFractionDigits:1}).format(amount)
}
