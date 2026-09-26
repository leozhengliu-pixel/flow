import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import * as Popover from '@radix-ui/react-popover'
import { Building2, Link2, X } from 'lucide-react'
import { toast } from 'sonner'
import { PropertyMenu } from '@/components/property/property-menu'
import { createCustomer, createCustomerRequest } from '@/lib/api'
import type { BootstrapData, Customer, CustomerRequest } from '@/types/flow'
import './embedded-customer-need-form.css'

export type EmbeddedCustomerNeedHost = 'issuePage' | 'projectPage' | 'customerPage'

export type EmbeddedCustomerNeedFormProps = {
  data: BootstrapData
  host: EmbeddedCustomerNeedHost
  issueId?: string
  projectId?: string
  /** Prefill / lock customer when on customer page. */
  customer?: Customer
  onCreated?: (request: CustomerRequest) => void | Promise<void>
  onCancel?: () => void
  className?: string
}

const IMPORTANT_PRIORITY = 1

/**
 * Embedded customer request composer for issues and projects: a customer pill, the request text,
 * a source link, and Cancel / Create. ⌘↵ creates, Esc cancels.
 */
export function EmbeddedCustomerNeedForm({
  data,
  host,
  issueId,
  projectId,
  customer: lockedCustomer,
  onCreated,
  onCancel,
  className,
}: EmbeddedCustomerNeedFormProps) {
  const [customerId, setCustomerId] = useState(lockedCustomer?.id ?? '')
  const [pendingName, setPendingName] = useState('')
  const [body, setBody] = useState('')
  const [sourceUrl, setSourceUrl] = useState('')
  const [saving, setSaving] = useState(false)
  const bodyRef = useRef<HTMLTextAreaElement>(null)

  const customers = useMemo(() => [...data.customers].sort((a, b) => a.name.localeCompare(b.name)), [data.customers])
  const selected = lockedCustomer ?? data.customers.find((item) => item.id === customerId)
  const customerLabel = selected?.name ?? (pendingName || 'Customer')

  useEffect(() => { bodyRef.current?.focus() }, [])

  const submit = async () => {
    if (saving) return
    if (!selected && !pendingName.trim()) {
      toast.error('Select a customer for this request')
      return
    }
    if (!body.trim() && !sourceUrl.trim()) {
      toast.error('Provide a request or specify a source')
      return
    }
    setSaving(true)
    try {
      const customer = selected ?? await createCustomer({ name: pendingName.trim() })
      const request = await createCustomerRequest({
        customerId: customer.id,
        body: body.trim(),
        source: 'manual',
        sourceUrl: sourceUrl.trim() || undefined,
        issueId,
        projectId,
      })
      toast.success('Customer request added')
      await onCreated?.(request)
      setBody('')
      setSourceUrl('')
      if (!lockedCustomer) {
        setCustomerId('')
        setPendingName('')
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Failed to save customer request')
    } finally {
      setSaving(false)
    }
  }

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    // Keys from the portaled pickers bubble here through React; only handle the form's own fields.
    if (event.defaultPrevented || !event.currentTarget.contains(event.target as Node)) return
    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) { event.preventDefault(); void submit() }
    else if (event.key === 'Escape' && onCancel) { event.preventDefault(); onCancel() }
  }

  return (
    <div className={`embedded-customer-need-form${className ? ` ${className}` : ''}`} data-host={host} onKeyDown={onKeyDown}>
      {!lockedCustomer && (
        <PropertyMenu
          label="Customer"
          ariaLabel="Customer"
          value={customerLabel}
          valueIsEntityName={Boolean(selected || pendingName)}
          selectedId={customerId}
          triggerRole="button"
          triggerClassName="embedded-customer-need-form__customer"
          trigger={<><CustomerMark customer={selected}/><span data-i18n-ignore={selected || pendingName ? true : undefined}>{customerLabel}</span></>}
          searchPlaceholder="Search customers…"
          emptyLabel="No customers"
          createOptionLabel={(name) => `Create new customer: “${name}”`}
          onCreate={async (name) => { setCustomerId(''); setPendingName(name); bodyRef.current?.focus() }}
          options={customers.map((item) => ({ id: item.id, label: item.name, icon: <CustomerMark customer={item}/>, i18nIgnore: true }))}
          onChange={(id) => { setCustomerId(id === customerId ? '' : id); setPendingName(''); bodyRef.current?.focus() }}
        />
      )}
      <textarea
        ref={bodyRef}
        aria-label="Request"
        className="embedded-customer-need-form__body"
        placeholder="Add request details"
        value={body}
        onChange={(event) => setBody(event.target.value)}
      />
      <div className="embedded-customer-need-form__footer">
        <SourceButton value={sourceUrl} onChange={setSourceUrl}/>
        <span className="embedded-customer-need-form__spacer"/>
        {onCancel && <button className="embedded-customer-need-form__cancel" type="button" onClick={onCancel}>Cancel</button>}
        <button className="embedded-customer-need-form__create" disabled={saving} type="button" onClick={() => void submit()}>Create</button>
      </div>
    </div>
  )
}

function CustomerMark({ customer }: { customer?: Pick<Customer, 'name' | 'logoUrl'> }) {
  if (customer?.logoUrl) return <img className="embedded-customer-need-form__logo" src={customer.logoUrl} alt=""/>
  return <Building2 aria-hidden="true" className="embedded-customer-need-form__logo" size={14}/>
}

/** "Source" pill that edits the request's source link in a small popover. */
function SourceButton({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState(value)
  const host = useMemo(() => {
    const trimmed = value.trim()
    if (!trimmed) return undefined
    try { return new URL(trimmed.includes('://') ? trimmed : `https://${trimmed}`).hostname } catch { return undefined }
  }, [value])
  const save = () => {
    const trimmed = draft.trim()
    if (!trimmed) { onChange(''); setOpen(false); return }
    const url = trimmed.includes('://') ? trimmed : `https://${trimmed}`
    try { new URL(url) } catch { toast.error('Invalid URL, please enter a valid URL'); return }
    onChange(url)
    setOpen(false)
  }
  return <Popover.Root open={open} onOpenChange={next => { setOpen(next); if (next) setDraft(value) }}>
    <Popover.Trigger asChild>
      <button className="embedded-customer-need-form__source" type="button" title={value || 'Add source'} aria-label={value ? `Source ${value}` : 'Add source'}>
        <Link2 size={14} aria-hidden="true"/><span>{host ? `via ${host}` : 'Source'}</span>
      </button>
    </Popover.Trigger>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating" className="embedded-customer-need-source-popover" side="bottom" align="start" sideOffset={4} collisionPadding={10}>
        <input
          autoFocus
          aria-label="Source URL"
          placeholder="Paste a link…"
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); save() } }}
        />
        {draft && <button type="button" aria-label="Clear source" onClick={() => { setDraft(''); onChange('') }}><X size={14}/></button>}
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

/** Map Important toggle ↔ REST priority (1 = important). */
export function importantFromPriority(priority?: number) {
  return (priority ?? 0) >= IMPORTANT_PRIORITY
}

export function priorityFromImportant(important: boolean) {
  return important ? IMPORTANT_PRIORITY : undefined
}
