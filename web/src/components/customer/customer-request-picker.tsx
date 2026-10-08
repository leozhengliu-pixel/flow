import { Command } from 'cmdk'
import { useEffect, useId, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from 'react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useI18n } from '@/i18n/i18n'
import type { Customer } from '@/types/flow'
import { CustomerLogo } from './customer-logo'
import { RequestPlusIcon } from './customer-request-glyphs'
import { customerPickOptions, type CustomerPickOption } from './customer-request-model'
import { closeCustomerRequestPicker, currentCustomerRequestPicker, customerRequestPickerHosts, subscribeCustomerRequestPicker, type CustomerPick } from './customer-request-events'
import './customer-request-picker.css'

export function CustomerPickIcon({ option }: { option: Pick<CustomerPickOption, 'kind' | 'customer'> }) {
  if (option.kind === 'customer') return <CustomerLogo customer={option.customer} size={16}/>
  if (option.kind === 'unknown') return <CustomerLogo customer={null} size={16}/>
  return <RequestPlusIcon/>
}

/** Renders the picker for `openCustomerRequestPicker`; the first mounted host on a page renders it. */
export function CustomerRequestPickerHost({ customers }: { customers: readonly Customer[] }) {
  const id = useId()
  const [, force] = useState(0)
  useEffect(() => {
    customerRequestPickerHosts.push(id)
    force(value => value + 1)
    return () => {
      customerRequestPickerHosts.splice(customerRequestPickerHosts.indexOf(id), 1)
      if (!customerRequestPickerHosts.length) closeCustomerRequestPicker()
    }
  }, [id])
  const request = useSyncExternalStore(subscribeCustomerRequestPicker, currentCustomerRequestPicker, () => undefined)
  if (customerRequestPickerHosts[0] !== id || !request) return null
  return <CustomerRequestPicker key={request.id} customers={customers} title={request.title} onClose={closeCustomerRequestPicker} onPick={pick => { closeCustomerRequestPicker(); request.onPick(pick) }}/>
}

export function CustomerRequestPicker({ customers, title, onPick, onClose }: { customers: readonly Customer[]; title?: string; onPick: (pick: CustomerPick) => void; onClose: () => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)
  const options = useMemo(() => customerPickOptions(customers, query), [customers, query])
  useEffect(() => {
    const timer = window.setTimeout(() => inputRef.current?.focus(), 60)
    return () => window.clearTimeout(timer)
  }, [])
  const label = (option: CustomerPickOption): ReactNode => {
    if (option.kind === 'customer') return <span data-i18n-ignore>{option.label}</span>
    if (option.kind === 'create') return <span data-i18n-ignore>{t('Create new customer: "{name}"').replace('{name}', query.trim())}</span>
    return t(option.label)
  }
  return <Dialog open onOpenChange={open => { if (!open) onClose() }}>
    <DialogContent className="command-dialog customer-request-picker" overlayClassName="command-overlay" onOpenAutoFocus={event => { event.preventDefault(); inputRef.current?.focus() }} onCloseAutoFocus={event => event.preventDefault()}
      // A menu that just closed (the issue "…" menu) returns focus to its trigger; keep the palette open.
      onFocusOutside={event => event.preventDefault()}>
      <DialogTitle className="sr-only">{t(title ?? 'Add customer request…')}</DialogTitle>
      <Command shouldFilter={false} loop>
        <div className="command-input">
          <Command.Input ref={inputRef} aria-label={t('Command menu')} autoFocus placeholder={t('Select customer…')} value={query} onValueChange={setQuery}/>
        </div>
        <Command.List>
          {options.map(option => <Command.Item key={option.id} value={option.id} data-kind={option.kind} onSelect={() => { if (option.pick) onPick(option.pick) }}>
            <span className="command-item-icon customer-request-picker__icon"><CustomerPickIcon option={option}/></span>
            <span className="customer-request-picker__label">{label(option)}</span>
          </Command.Item>)}
        </Command.List>
      </Command>
    </DialogContent>
  </Dialog>
}
