import { useMemo, useState } from 'react'
import { Link2, Plus, X } from 'lucide-react'
import { toast } from 'sonner'
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
 * Embedded additional customer-need create form (LS-0023).
 * Hosts: issue + project (+ reusable on customer). Exposes sourceUrl and Important↔priority.
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
  const [customerName, setCustomerName] = useState('')
  const [body, setBody] = useState('')
  const [sourceUrl, setSourceUrl] = useState('')
  const [important, setImportant] = useState(false)
  const [saving, setSaving] = useState(false)

  const customers = useMemo(
    () => [...data.customers].sort((a, b) => a.name.localeCompare(b.name)),
    [data.customers],
  )

  const canSubmit = Boolean(body.trim() && (lockedCustomer || customerId || customerName.trim()))

  const submit = async () => {
    if (!canSubmit || saving) return
    setSaving(true)
    try {
      let customer = lockedCustomer ?? data.customers.find((item) => item.id === customerId)
      if (!customer && customerName.trim()) {
        customer = await createCustomer({ name: customerName.trim() })
      }
      if (!customer) throw new Error('Select or create a customer')
      const request = await createCustomerRequest({
        customerId: customer.id,
        body: body.trim(),
        source: 'manual',
        sourceUrl: sourceUrl.trim() || undefined,
        issueId,
        projectId,
        priority: important ? IMPORTANT_PRIORITY : undefined,
      })
      await onCreated?.(request)
      setBody('')
      setSourceUrl('')
      setImportant(false)
      if (!lockedCustomer) {
        setCustomerId('')
        setCustomerName('')
      }
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Could not add request')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div
      className={`embedded-customer-need-form${className ? ` ${className}` : ''}`}
      data-host={host}
    >
      <header>
        <strong>Add customer request</strong>
        {onCancel && (
          <button aria-label="Discard" type="button" onClick={onCancel}>
            <X size={14} />
          </button>
        )}
      </header>
      {!lockedCustomer && (
        <div className="embedded-customer-need-form__row">
          <label>
            <span>Customer</span>
            <select
              aria-label="Customer"
              value={customerId}
              onChange={(event) => {
                setCustomerId(event.target.value)
                setCustomerName('')
              }}
            >
              <option value="">Select customer…</option>
              {customers.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))}
            </select>
          </label>
          {!customerId && (
            <label>
              <span>Or create</span>
              <input
                aria-label="New customer name"
                placeholder="New customer name"
                value={customerName}
                onChange={(event) => setCustomerName(event.target.value)}
              />
            </label>
          )}
        </div>
      )}
      <label className="embedded-customer-need-form__body">
        <span>Request</span>
        <textarea
          aria-label="Request details"
          placeholder="Add request details"
          rows={3}
          value={body}
          onChange={(event) => setBody(event.target.value)}
        />
      </label>
      <label className="embedded-customer-need-form__source">
        <span>
          <Link2 size={13} /> Source URL
        </span>
        <input
          aria-label="Source URL"
          placeholder="https://…"
          type="url"
          value={sourceUrl}
          onChange={(event) => setSourceUrl(event.target.value)}
        />
      </label>
      <div className="embedded-customer-need-form__footer">
        <button
          aria-pressed={important}
          className={`embedded-customer-need-form__important${important ? ' is-on' : ''}`}
          type="button"
          onClick={() => setImportant((value) => !value)}
        >
          Important
        </button>
        <span className="embedded-customer-need-form__spacer" />
        {onCancel && (
          <button className="embedded-customer-need-form__ghost" type="button" onClick={onCancel}>
            Discard
          </button>
        )}
        <button
          className="embedded-customer-need-form__primary"
          disabled={!canSubmit || saving}
          type="button"
          onClick={() => void submit()}
        >
          <Plus size={13} />
          {saving ? 'Adding…' : 'Add request'}
        </button>
      </div>
    </div>
  )
}

/** Map Important toggle ↔ REST priority (1 = important). */
export function importantFromPriority(priority?: number) {
  return (priority ?? 0) >= IMPORTANT_PRIORITY
}

export function priorityFromImportant(important: boolean) {
  return important ? IMPORTANT_PRIORITY : undefined
}
