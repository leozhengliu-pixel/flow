import { useEffect, useState, type FormEvent, type ReactNode } from 'react'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { SelectControl } from '@/components/ui/select-control'
import type { Customer, CustomerMutationInput, User } from '@/types/flow'

type CustomerDraft = {
  name: string
  logoUrl: string
  ownerId: string
  status: Customer['status']
  tier: string
  annualRevenue: string
  size: string
  domains: string
}

const emptyDraft: CustomerDraft = { name: '', logoUrl: '', ownerId: '', status: 'active', tier: '', annualRevenue: '', size: '', domains: '' }

export function CustomerDialog({ open, users, customer, onOpenChange, onSubmit }: {
  open: boolean
  users: User[]
  customer?: Customer
  onOpenChange: (open: boolean) => void
  onSubmit: (input: CustomerMutationInput & { name: string }) => Promise<void>
}) {
  const [draft, setDraft] = useState<CustomerDraft>(emptyDraft)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!open) return
    setDraft(customer ? {
      name: customer.name,
      logoUrl: customer.logoUrl ?? '',
      ownerId: customer.ownerId ?? '',
      status: customer.status,
      tier: customer.tier ?? '',
      annualRevenue: customer.annualRevenue ? String(customer.annualRevenue) : '',
      size: customer.size ? String(customer.size) : '',
      domains: customer.domains.join(', '),
    } : emptyDraft)
  }, [customer, open])

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!draft.name.trim() || saving) return
    setSaving(true)
    try {
      await onSubmit({
        name: draft.name.trim(),
        logoUrl: draft.logoUrl.trim() || undefined,
        ownerId: draft.ownerId || undefined,
        status: draft.status,
        tier: draft.tier || undefined,
        annualRevenue: draft.annualRevenue ? Number(draft.annualRevenue) : undefined,
        size: draft.size ? Number(draft.size) : undefined,
        domains: draft.domains.split(',').map(value => value.trim()).filter(Boolean),
      })
      onOpenChange(false)
    } finally {
      setSaving(false)
    }
  }

  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent className="workspace-customer-dialog">
      <DialogTitle>{customer ? 'Edit customer' : 'Create customer'}</DialogTitle>
      <form onSubmit={submit}>
        <div className="workspace-customer-logo">
          {draft.logoUrl ? <img src={draft.logoUrl} alt=""/> : <span>{draft.name.trim().slice(0, 1).toUpperCase() || 'C'}</span>}
          <span><strong>Logo</strong><small>Use a square image for the best result.</small></span>
        </div>
        <div className="workspace-customer-form-grid">
          <Field label="Name"><input autoFocus aria-label="Name" placeholder="Customer name" value={draft.name} onChange={event => setDraft(value => ({ ...value, name: event.target.value }))}/></Field>
          <Field label="Owner"><SelectControl className="workspace-customer-select" label="Owner" value={draft.ownerId} onChange={ownerId => setDraft(value => ({ ...value, ownerId }))} options={[{value:'',label:'No owner'},...users.map(user=>({value:user.id,label:user.displayName,entityName:true}))]}/></Field>
          <Field label="Status"><SelectControl className="workspace-customer-select" label="Status" value={draft.status} onChange={status => setDraft(value => ({ ...value, status: status as Customer['status'] }))} options={[{value:'active',label:'Active'},{value:'inactive',label:'Inactive'}]}/></Field>
          <Field label="Tier"><SelectControl className="workspace-customer-select" label="Tier" value={draft.tier} onChange={tier => setDraft(value => ({ ...value, tier }))} options={[{value:'',label:'No tier'},{value:'Enterprise',label:'Enterprise'},{value:'Mid-market',label:'Mid-market'},{value:'Small business',label:'Small business'}]}/></Field>
          <Field label="Annual revenue"><div className="workspace-money-input"><span>$</span><input aria-label="Annual revenue" inputMode="decimal" value={draft.annualRevenue} onChange={event => setDraft(value => ({ ...value, annualRevenue: event.target.value.replace(/[^\d.]/g, '') }))}/></div></Field>
          <Field label="Size"><input aria-label="Size" inputMode="numeric" value={draft.size} onChange={event => setDraft(value => ({ ...value, size: event.target.value.replace(/\D/g, '') }))}/></Field>
        </div>
        <Field label="Domains"><input aria-label="Domains" placeholder="customer.com, example.org" value={draft.domains} onChange={event => setDraft(value => ({ ...value, domains: event.target.value }))}/></Field>
        <Field label="Logo URL"><input aria-label="Logo URL" placeholder="https://..." value={draft.logoUrl} onChange={event => setDraft(value => ({ ...value, logoUrl: event.target.value }))}/></Field>
        <footer><button type="button" onClick={() => onOpenChange(false)}>Cancel</button><button className="is-primary" type="submit" disabled={!draft.name.trim() || saving}>{saving ? 'Saving…' : customer ? 'Save changes' : 'Create customer'}</button></footer>
      </form>
    </DialogContent>
  </Dialog>
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return <label className="workspace-field"><span>{label}</span>{children}</label>
}
