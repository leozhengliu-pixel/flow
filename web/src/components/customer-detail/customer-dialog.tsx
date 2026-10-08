import { useEffect, useId, useMemo, useRef, useState, type ClipboardEvent, type DragEvent, type FormEvent, type KeyboardEvent, type ReactNode } from 'react'
import { Trash2, X } from 'lucide-react'

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { SelectControl } from '@/components/ui/select-control'
import { CustomerStatusIcon, CustomerTierIcon } from '@/components/customer/customer-status-icon'
import { useI18n } from '@/i18n/i18n'
import type { Customer, CustomerMutationInput, User } from '@/types/flow'
import { hasCustomerDraftErrors, normalizeDomain, validateCustomerDraft, type CustomerDraftErrors } from './customer-form-model'
import './customer-dialog.css'

type CustomerDraft = {
  name: string
  logoUrl: string
  ownerId: string
  status: Customer['status']
  tier: string
  annualRevenue: string
  size: string
  domains: string[]
}
type CustomerOption = { id?: string; name: string; color?: string; position?: number; archivedAt?: string }

const LOGO_TYPES = ['image/png', 'image/jpeg', 'image/webp', 'image/gif']
const LOGO_SIZE = 128
const emptyDraft: CustomerDraft = { name: '', logoUrl: '', ownerId: '', status: 'active', tier: '', annualRevenue: '', size: '', domains: [''] }
const optionValue = (option: CustomerOption) => option.id ?? option.name
const byPosition = (left: CustomerOption, right: CustomerOption) => (left.position ?? 0) - (right.position ?? 0)
/** A stored status/tier value is an id or, for older customers, a name. */
const findOption = (options: CustomerOption[], value: string) => options.find(option => option.id === value) ?? options.find(option => option.name.toLowerCase() === value.toLowerCase())

/**
 * Create / edit customer modal, laid out like Linear's (580px; header title and
 * Discard; logo picker; Name/Owner, Status/Tier, Annual revenue/Size in two
 * columns; a Domains list with "Add domain"; Cancel and "Create customer").
 */
export function CustomerDialog({ open, users, customer, customers = [], statuses = [], tiers = [], onOpenChange, onSubmit, currency = 'USD', revenueLabel, monthlyRevenue = false }: {
  open: boolean
  users: User[]
  customer?: Customer
  /** Existing customers, to warn about a duplicate name like Linear. */
  customers?: Pick<Customer, 'id' | 'name'>[]
  /** Workspace customer statuses (for their colours) and tiers. */
  statuses?: CustomerOption[]
  tiers?: CustomerOption[]
  currency?: string
  /** The revenue field's label ("Annual revenue" or "Monthly revenue", per the workspace setting). */
  revenueLabel?: string
  /** Revenue is entered per month and stored annualised (Settings › Customer requests › Revenue: Monthly). */
  monthlyRevenue?: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (input: CustomerMutationInput & { name: string }) => Promise<void>
}) {
  const { t, locale } = useI18n()
  const id = useId()
  const [draft, setDraft] = useState<CustomerDraft>(emptyDraft)
  const [errors, setErrors] = useState<CustomerDraftErrors>({})
  const [submitted, setSubmitted] = useState(false)
  const [saving, setSaving] = useState(false)

  const statusOptions = useMemo(() => statuses.filter(status => !status.archivedAt).sort(byPosition), [statuses])
  const tierOptions = useMemo(() => tiers.filter(tier => !tier.archivedAt).sort(byPosition), [tiers])
  const defaultStatus = statusOptions[0]
  useEffect(() => {
    if (!open) return
    setErrors({})
    setSubmitted(false)
    const status = (value: string) => { const match = findOption(statusOptions, value); return match ? optionValue(match) : value }
    const tier = (value?: string) => { if (!value) return ''; const match = findOption(tierOptions, value); return match ? optionValue(match) : value }
    setDraft(customer ? {
      name: customer.name,
      logoUrl: customer.logoUrl ?? '',
      ownerId: customer.ownerId ?? '',
      status: status(customer.status),
      tier: tier(customer.tier),
      annualRevenue: customer.annualRevenue ? String(Math.round(monthlyRevenue ? customer.annualRevenue / 12 : customer.annualRevenue)) : '',
      size: customer.size ? String(customer.size) : '',
      domains: customer.domains.length ? [...customer.domains] : [''],
    } : { ...emptyDraft, status: defaultStatus ? optionValue(defaultStatus) : 'active' })
    // Statuses only seed the draft when the dialog opens.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [customer, open])

  const update = (patch: Partial<CustomerDraft>) => setDraft(current => {
    const next = { ...current, ...patch }
    if (submitted) setErrors(validateCustomerDraft(next, t))
    return next
  })
  const duplicate = useMemo(() => {
    const name = draft.name.trim().toLocaleLowerCase()
    return name ? customers.find(item => item.id !== customer?.id && item.name.trim().toLocaleLowerCase() === name) : undefined
  }, [customer?.id, customers, draft.name])
  const statusSelect = useMemo(() => {
    const options = statusOptions.map(status => ({ value: optionValue(status), label: status.name, entityName: true, icon: <CustomerStatusIcon className="customer-form__status-icon" color={status.color}/> }))
    if (!options.length) return [{ value: 'active', label: locale === 'zh-CN' ? '活跃' : 'Active', icon: <CustomerStatusIcon className="customer-form__status-icon"/> }, { value: 'inactive', label: t('Inactive'), icon: <CustomerStatusIcon className="customer-form__status-icon" monochrome/> }]
    return draft.status && !options.some(option => option.value === draft.status) ? [...options, { value: draft.status, label: draft.status, entityName: true, icon: <CustomerStatusIcon className="customer-form__status-icon" monochrome/> }] : options
  }, [draft.status, locale, statusOptions, t])
  const tierSelect = useMemo(() => {
    const options = tierOptions.map(tier => ({ value: optionValue(tier), label: tier.name, entityName: true, icon: <CustomerTierIcon className="customer-form__tier-icon" size={14} style={tier.color ? { color: tier.color } : undefined}/> }))
    return draft.tier && !options.some(option => option.value === draft.tier) ? [...options, { value: draft.tier, label: draft.tier, entityName: true, icon: undefined }] : options
  }, [draft.tier, tierOptions])
  const symbol = currencySymbol(currency)

  const submit = async (event?: FormEvent) => {
    event?.preventDefault()
    if (saving) return
    setSubmitted(true)
    const nextErrors = validateCustomerDraft(draft, t)
    setErrors(nextErrors)
    if (hasCustomerDraftErrors(nextErrors)) return
    setSaving(true)
    try {
      await onSubmit({
        name: draft.name.trim(),
        logoUrl: draft.logoUrl.trim() || (customer?.logoUrl ? '' : undefined),
        ownerId: draft.ownerId || (customer?.ownerId ? '' : undefined),
        status: draft.status,
        tier: draft.tier || (customer?.tier ? '' : undefined),
        annualRevenue: draft.annualRevenue ? Number(draft.annualRevenue) * (monthlyRevenue ? 12 : 1) : customer?.annualRevenue ? 0 : undefined,
        size: draft.size ? Number(draft.size) : customer?.size ? 0 : undefined,
        domains: draft.domains.map(normalizeDomain).filter(Boolean),
      })
      onOpenChange(false)
    } finally {
      setSaving(false)
    }
  }
  const onKeyDown = (event: KeyboardEvent<HTMLFormElement>) => {
    if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') { event.preventDefault(); void submit() }
  }

  return <Dialog open={open} onOpenChange={onOpenChange}>
    <DialogContent aria-describedby={undefined} className="customer-form" closeLabel={t('Discard')}>
      <form noValidate onKeyDown={onKeyDown} onSubmit={event => void submit(event)}>
        <header className="customer-form__header"><DialogTitle>{t(customer ? 'Edit customer' : 'Create customer')}</DialogTitle></header>
        <div className="customer-form__body">
          <CustomerLogoField error={errors.logo} value={draft.logoUrl} onChange={logoUrl => update({ logoUrl })} onError={logo => setErrors(current => ({ ...current, logo }))}/>
          <div className="customer-form__section customer-form__grid">
            <Field error={errors.name} htmlFor={`${id}-name`} label={t('Name')}>
              <input aria-invalid={errors.name ? true : undefined} autoComplete="off" autoFocus id={`${id}-name`} placeholder={t('Customer name')} value={draft.name} onChange={event => update({ name: event.target.value })}/>
            </Field>
            <Field label={t('Owner')}>
              <SelectControl className="customer-form__select" label="Owner" value={draft.ownerId} onChange={ownerId => update({ ownerId })} options={[{ value: '', label: t('No owner') }, ...users.map(user => ({ value: user.id, label: user.displayName || user.name, entityName: true }))]}/>
            </Field>
          </div>
          <div className="customer-form__section customer-form__grid">
            <Field label={t('Status')}>
              <SelectControl className="customer-form__select is-value" label="Status" value={draft.status} onChange={status => update({ status })} options={statusSelect}/>
            </Field>
            <Field label={t('Tier')}>
              <SelectControl className="customer-form__select is-value" label="Tier" value={draft.tier} onChange={tier => update({ tier })} options={[{ value: '', label: t('No tier') }, ...tierSelect]}/>
            </Field>
          </div>
          <div className="customer-form__section customer-form__grid">
            <Field error={errors.annualRevenue} htmlFor={`${id}-revenue`} label={revenueLabel ?? t('Annual revenue')}>
              <div className="customer-form__number">
                {symbol && <span aria-hidden="true" data-i18n-ignore>{symbol}</span>}
                <input aria-invalid={errors.annualRevenue ? true : undefined} aria-label={revenueLabel ?? t('Annual revenue')} autoComplete="off" id={`${id}-revenue`} style={symbol ? { paddingLeft: `calc(17px + ${symbol.length}ch)` } : undefined} inputMode="numeric" value={formatInteger(draft.annualRevenue)} onChange={event => update({ annualRevenue: digits(event.target.value) })} onKeyDown={event => stepNumber(event, draft.annualRevenue, annualRevenue => update({ annualRevenue }))}/>
              </div>
            </Field>
            <Field error={errors.size} htmlFor={`${id}-size`} label={t('Size')}>
              <div className="customer-form__number">
                <input aria-invalid={errors.size ? true : undefined} aria-label={t('Size')} autoComplete="off" id={`${id}-size`} inputMode="numeric" value={formatInteger(draft.size)} onChange={event => update({ size: digits(event.target.value) })} onKeyDown={event => stepNumber(event, draft.size, size => update({ size }))}/>
              </div>
            </Field>
          </div>
          <div className="customer-form__section customer-form__domains">
            <div className="customer-form__domains-header">
              <label htmlFor={`${id}-domain-0`}>{t('Domains')}</label>
              <button className="customer-form__link-button" onClick={() => update({ domains: [...draft.domains, ''] })} type="button">{t('Add domain')}</button>
            </div>
            {draft.domains.map((domain, index) => <div className="customer-form__domain" key={index}>
              <div className="customer-form__domain-row">
                <input aria-invalid={errors.domains?.[index] ? true : undefined} aria-label={t('Domains')} autoComplete="off" id={`${id}-domain-${index}`} placeholder="customer.com" value={domain} onChange={event => update({ domains: draft.domains.map((value, position) => position === index ? event.target.value : value) })}/>
                {draft.domains.length > 1 && <button aria-label={t('Remove domain')} className="customer-form__icon-button" onClick={() => update({ domains: draft.domains.filter((_, position) => position !== index) })} title={t('Remove domain')} type="button"><Trash2 size={14}/></button>}
              </div>
              {errors.domains?.[index] && <p className="customer-form__error" role="alert">{errors.domains[index]}</p>}
            </div>)}
            {errors.domainsAll && <p className="customer-form__error" role="alert">{errors.domainsAll}</p>}
          </div>
          {duplicate && <p className="customer-form__warning" role="status"><span>{t('A customer with this name already exists:')}</span> <strong data-i18n-ignore>{duplicate.name}</strong></p>}
        </div>
        <footer className="customer-form__footer">
          <button className="customer-form__button" onClick={() => onOpenChange(false)} type="button">{t('Cancel')}</button>
          <button className="customer-form__button is-primary" disabled={saving} type="submit">{saving ? t('Saving…') : t(customer ? 'Update customer' : 'Create customer')}</button>
        </footer>
      </form>
    </DialogContent>
  </Dialog>
}

function Field({ children, error, htmlFor, label }: { children: ReactNode; error?: string; htmlFor?: string; label: string }) {
  return <div className="customer-form__field">
    {htmlFor ? <label htmlFor={htmlFor}>{label}</label> : <span className="customer-form__label">{label}</span>}
    {children}
    {error && <p className="customer-form__error" role="alert">{error}</p>}
  </div>
}

/** Linear's logo picker: a 40px drop zone (click, drop or paste an image or image URL) with a remove button. */
function CustomerLogoField({ error, value, onChange, onError }: { error?: string; value: string; onChange: (value: string) => void; onError: (error?: string) => void }) {
  const { t } = useI18n()
  const inputRef = useRef<HTMLInputElement>(null)
  const [dragging, setDragging] = useState(false)
  const acceptFile = async (file?: File | null) => {
    if (!file) return
    if (!LOGO_TYPES.includes(file.type)) { onError(t('Upload a PNG, JPEG, WebP or GIF image.')); return }
    try { onChange(await logoDataUrl(file)); onError(undefined) }
    catch { onError(t('Could not read this image.')) }
  }
  const onDrop = (event: DragEvent<HTMLButtonElement>) => {
    event.preventDefault()
    setDragging(false)
    void acceptFile(event.dataTransfer.files[0])
  }
  const onPaste = (event: ClipboardEvent<HTMLButtonElement>) => {
    const file = [...event.clipboardData.files].find(item => item.type.startsWith('image/'))
    if (file) { event.preventDefault(); void acceptFile(file); return }
    const text = event.clipboardData.getData('text').trim()
    if (/^https?:\/\/\S+$/i.test(text)) { event.preventDefault(); onChange(text); onError(undefined) }
  }
  return <div className="customer-form__section customer-form__logo-row">
    <div className="customer-form__logo">
      <button aria-label={t('Upload logo')} className="customer-form__logo-drop" data-dragging={dragging || undefined} onClick={() => inputRef.current?.click()} onDragLeave={() => setDragging(false)} onDragOver={event => { event.preventDefault(); setDragging(true) }} onDrop={onDrop} onPaste={onPaste} title={t('Upload logo')} type="button">
        {value ? <img alt="" src={value}/> : <UploadIcon/>}
        <span aria-hidden="true" className="customer-form__logo-overlay"><EditIcon/></span>
      </button>
      {value && <button aria-label={t('Remove image')} className="customer-form__logo-clear" onClick={() => { onChange(''); onError(undefined) }} title={t('Remove image')} type="button"><X size={10}/></button>}
      <input accept={LOGO_TYPES.join(',')} hidden onChange={event => { void acceptFile(event.target.files?.[0]); event.target.value = '' }} ref={inputRef} tabIndex={-1} type="file"/>
    </div>
    <div className="customer-form__logo-copy">
      <strong>{t('Logo')}</strong>
      <small>{t('Recommended size is 128 x 128px.')}</small>
      {error && <p className="customer-form__error" role="alert">{error}</p>}
    </div>
  </div>
}

function UploadIcon() {
  return <svg aria-hidden="true" fill="currentColor" height="16" viewBox="0 0 16 16" width="16"><path d="M2.615 9.145v2.546c0 .984.844 1.782 1.885 1.782h7c1.04 0 1.885-.798 1.885-1.782V9.145c0-.421.361-.763.807-.763.446 0 .808.342.808.763v2.546C15 13.518 13.433 15 11.5 15h-7C2.567 15 1 13.518 1 11.69V9.146c0-.421.362-.763.808-.763.446 0 .807.342.807.763Z"/><path d="M7 5H4.5c-.444 0-.667-.568-.353-.9l3.5-2.946a.482.482 0 0 1 .706 0l3.5 2.946c.315.332.091.9-.354.9H9v5a1 1 0 1 1-2 0V5Z"/></svg>
}

/** Linear's logo hover glyph (a pencil). */
function EditIcon() {
  return <svg aria-hidden="true" fill="currentColor" height="16" viewBox="0 0 16 16" width="16"><path d="M10.1805 3.34195L4.14166 9.416C5.32948 9.77021 6.29238 10.6629 6.74008 11.8184L12.6877 5.8425C11.6642 5.22123 10.8043 4.36352 10.1805 3.34195Z"/><path d="M13.7391 4.71631C14.1575 4.02948 14.0727 3.11738 13.4846 2.5219C12.8908 1.92072 11.9784 1.83892 11.298 2.27649C11.8547 3.31132 12.7037 4.15999 13.7391 4.71631Z"/><path d="M3.03104 10.7502C4.30296 10.7658 5.36645 11.7423 5.49783 13.0114C4.83268 13.426 3.40197 13.7922 2.53114 13.9886C2.2001 14.0632 1.92026 13.7602 2.02075 13.4373C2.25326 12.6902 2.64592 11.5136 3.03104 10.7502Z"/></svg>
}

function digits(value: string) { return value.replace(/\D/g, '').replace(/^0+(?=\d)/, '') }
function formatInteger(value: string) { return value ? Number(value).toLocaleString('en-US') : '' }
function stepNumber(event: KeyboardEvent<HTMLInputElement>, value: string, onChange: (value: string) => void) {
  if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return
  event.preventDefault()
  const current = Number(value || 0)
  onChange(String(Math.max(0, current + (event.key === 'ArrowUp' ? 1 : -1))))
}
function currencySymbol(currency: string) {
  try { return new Intl.NumberFormat('en-US', { style: 'currency', currency, currencyDisplay: 'narrowSymbol' }).formatToParts(0).find(part => part.type === 'currency')?.value ?? currency }
  catch { return currency }
}
/** Reads an image and scales it to fit 128×128 (Linear's recommended logo size) as a PNG data URL. */
async function logoDataUrl(file: File): Promise<string> {
  const source = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
  const image = await new Promise<HTMLImageElement>((resolve, reject) => {
    const element = new Image()
    element.onload = () => resolve(element)
    element.onerror = reject
    element.src = source
  })
  const scale = Math.min(1, LOGO_SIZE / Math.max(image.naturalWidth || LOGO_SIZE, image.naturalHeight || LOGO_SIZE))
  const canvas = document.createElement('canvas')
  canvas.width = Math.max(1, Math.round((image.naturalWidth || LOGO_SIZE) * scale))
  canvas.height = Math.max(1, Math.round((image.naturalHeight || LOGO_SIZE) * scale))
  const context = canvas.getContext('2d')
  if (!context) return source
  context.drawImage(image, 0, 0, canvas.width, canvas.height)
  return canvas.toDataURL('image/png')
}
