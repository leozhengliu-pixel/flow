import { useEffect, useState, type ReactNode } from 'react'
import { toast } from 'sonner'

import { CustomerStatusIcon } from '@/components/customer/customer-status-icon'
import { findCustomerStatus, findCustomerTier } from '@/components/customer/customer-status-lookup'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useI18n } from '@/i18n/i18n'
import { customerRevenueLabel } from '@/lib/customer-settings'
import { mergeCustomer } from '@/lib/api'
import type { BootstrapData, Customer } from '@/types/flow'
import { CustomerLogo } from '@/components/customer/customer-logo'
import { formatCustomerSize } from './customer-page-model'

/**
 * Linear's "Merge with…" modal: source → target logos with "Switch direction", a Merge / With /
 * Result summary, and a typed "merge" confirmation. The target keeps its name, status, tier,
 * revenue and size; requests move over and domains are combined.
 */
export function CustomerMergeDialog({ data, source: initialSource, target: initialTarget, open, onOpenChange, onMerged }: { data: BootstrapData; source: Customer; target?: Customer; open: boolean; onOpenChange: (open: boolean) => void; onMerged: (target: Customer, source: Customer) => Promise<void> | void }) {
  const { t, locale } = useI18n()
  const [source, setSource] = useState(initialSource)
  const [target, setTarget] = useState(initialTarget)
  const [confirmation, setConfirmation] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  useEffect(() => {
    if (!open) return
    setSource(initialSource); setTarget(initialTarget); setConfirmation(''); setError('')
  }, [open, initialSource, initialTarget])
  if (!target) return null
  const settings = data.workspaceSettings.featureSettings
  const requestCount = (customer: Customer) => data.customerRequests.filter(request => request.customerId === customer.id).length
  const currency = new Intl.NumberFormat(locale, { style: 'currency', currency: /^[A-Z]{3}$/.test(settings?.customerRevenueCurrency ?? '') ? settings.customerRevenueCurrency : 'USD', maximumFractionDigits: 0 })
  const revenue = (customer: Customer) => customer.annualRevenue ? currency.format(settings?.customerRevenueFormat === 'monthly' ? Math.round(customer.annualRevenue / 12) : customer.annualRevenue) : undefined
  const status = (customer: Customer) => findCustomerStatus(data.customerStatuses, customer.status)
  const tier = (customer: Customer) => findCustomerTier(data.customerTiers, customer.tier)?.name ?? customer.tier
  const submit = async () => {
    if (confirmation.trim() !== 'merge') { setError(t('Please type the required string')); return }
    setSaving(true)
    try {
      const merged = await mergeCustomer(source.id, target.id)
      toast.success(t('Customers merged'))
      onOpenChange(false)
      await onMerged(merged, source)
    } catch (cause) {
      toast.error(t('Unable to merge customers'), { description: cause instanceof Error ? cause.message : t('Something unexpected went wrong when merging customers.') })
    } finally {
      setSaving(false)
    }
  }
  const statusCell = (customer: Customer) => { const value = status(customer); return value ? <><CustomerStatusIcon color={value.color}/><span data-i18n-ignore>{value.name}</span></> : undefined }
  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="customer-merge" closeLabel={t('Discard')}>
      <DialogTitle className="customer-merge__sr">{t('Merge customers')}</DialogTitle>
      <div className="customer-merge__hero">
        <div className="customer-merge__logos">
          <span className="customer-merge__logo"><CustomerLogo customer={source} size={32}/></span>
          <span className="customer-merge__arrow">→</span>
          <span className="customer-merge__logo"><CustomerLogo customer={target} size={32}/></span>
        </div>
        <button type="button" className="customer-merge__switch" onClick={() => { setSource(target); setTarget(source) }}>{t('Switch direction')}</button>
      </div>
      <div className="customer-merge__summary" role="table" aria-label={t('Merge summary')}>
        <MergeRow header label="" from={t('Merge')} to={t('With')} result={t('Result')}/>
        <MergeRow label={t('Name')} from={<><CustomerLogo customer={source}/><span data-i18n-ignore>{source.name}</span></>} to={<><CustomerLogo customer={target}/><span data-i18n-ignore>{target.name}</span></>} result={<><CustomerLogo customer={target}/><span data-i18n-ignore>{target.name}</span></>}/>
        <MergeRow label={t('Status')} from={statusCell(source)} to={statusCell(target)} result={statusCell(target)}/>
        <MergeRow label={t('Tier')} from={tier(source)} to={tier(target)} result={tier(target)}/>
        <MergeRow label={t('Requests')} from={formatCustomerSize(requestCount(source), locale)} to={formatCustomerSize(requestCount(target), locale)} result={formatCustomerSize(requestCount(source) + requestCount(target), locale)}/>
        <MergeRow label={t(customerRevenueLabel(settings))} from={revenue(source)} to={revenue(target)} result={revenue(target)}/>
        <MergeRow label={t('Size')} from={source.size ? formatCustomerSize(source.size, locale) : undefined} to={target.size ? formatCustomerSize(target.size, locale) : undefined} result={target.size ? formatCustomerSize(target.size, locale) : undefined}/>
        <MergeRow last label={t('Domains')} from={domains(source.domains)} to={domains(target.domains)} result={domains([...new Set([...source.domains, ...target.domains])].sort())}/>
      </div>
      <form className="customer-merge__confirm" onSubmit={event => { event.preventDefault(); void submit() }}>
        <p>{t('Type {word} to continue').split('{word}').map((part, index) => <span key={index}>{index > 0 && <strong data-i18n-ignore>“merge”</strong>}{part}</span>)}</p>
        <div>
          <input aria-label={t('Type merge to continue')} placeholder="merge" value={confirmation} onChange={event => { setConfirmation(event.target.value); setError('') }} data-i18n-ignore/>
          <button type="button" className="customer-merge__cancel" onClick={() => onOpenChange(false)}>{t('Cancel')}</button>
          <button type="submit" className="customer-merge__submit" disabled={saving}>{t('Merge')}</button>
        </div>
        {error && <span className="customer-merge__error" role="alert">{error}</span>}
      </form>
    </DialogContent>
  </Dialog>
}

function domains(values: string[]) {
  return values.length ? <span className="customer-merge__domains" data-i18n-ignore>{values.map(value => <span key={value} title={value}>{value}</span>)}</span> : undefined
}

function MergeRow({ label, from, to, result, header = false, last = false }: { label: string; from?: ReactNode; to?: ReactNode; result?: ReactNode; header?: boolean; last?: boolean }) {
  const cell = (value: ReactNode) => value ?? '--'
  return <div className="customer-merge__row" role="row" data-header={header || undefined} data-last={last || undefined}>
    <span role="rowheader">{label}</span>
    <span role="cell">{cell(from)}</span>
    <span role="cell">{cell(to)}</span>
    <span role="cell">{cell(result)}</span>
  </div>
}
