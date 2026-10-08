import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { toast } from 'sonner'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { LinearDropdownMenuContent, LinearMenuItem, LinearMenuOptions, type LinearMenuOption } from '@/components/ui/row-context-menu'
import { FlowTooltip } from '@/components/ui/tooltip'
import { isMacPlatform } from '@/components/project-detail/project-detail-shortcuts'
import { useI18n } from '@/i18n/i18n'
import { createCustomerRequest, updateCustomerRequest, uploadCustomerRequestAttachment } from '@/lib/api'
import type { BootstrapData, Customer, CustomerRequest } from '@/types/flow'
import { CustomerLogo } from './customer-logo'
import { AttachIcon, ClearIcon, RequestPlusIcon, SourceLinkIcon } from './customer-request-glyphs'
import { IMPORTANT_PRIORITY, looksLikeUrl, normalizeSourceUrl, sourceHost } from './customer-request-model'
import './embedded-customer-need-form.css'

export type EmbeddedCustomerNeedHost = 'issuePage' | 'projectPage' | 'customerPage'

export type EmbeddedCustomerNeedFormProps = {
  data: BootstrapData
  host: EmbeddedCustomerNeedHost
  issueId?: string
  projectId?: string
  /** Prefill and lock the customer (customer page, "New request from …"); hides the customer button. */
  customer?: Customer
  /** Customer chosen in the "Select customer…" step. */
  initialCustomerId?: string
  /** Name typed in the "Select customer…" step: the customer is created when the request is saved. */
  pendingCustomerName?: string
  /** Link pasted in the "Select customer…" step ("Add link as source"). */
  initialSourceUrl?: string
  /** Edit an existing request ("Edit request"): no customer button, the button reads Save. */
  request?: CustomerRequest
  /** `card` (default) draws the bordered surface; `inline` sits inside an expanded row. */
  variant?: 'card' | 'inline'
  onCreated?: (request: CustomerRequest) => void | Promise<void>
  onSaved?: (request: CustomerRequest) => void | Promise<void>
  onCancel?: () => void
  className?: string
}

/**
 * Linear's customer request composer (AdditionalCustomerNeedCreateForm.J): customer button, the
 * request text, then Source, attach, Cancel and Create. ⌘↵ submits; Esc and Cancel discard, asking
 * first when something was entered. A customer typed in the picker is created with the request.
 */
export function EmbeddedCustomerNeedForm({
  data,
  host,
  issueId,
  projectId,
  customer: lockedCustomer,
  initialCustomerId,
  pendingCustomerName,
  initialSourceUrl,
  request,
  variant = 'card',
  onCreated,
  onSaved,
  onCancel,
  className,
}: EmbeddedCustomerNeedFormProps) {
  const { t } = useI18n()
  const editing = Boolean(request)
  const hideCustomer = editing || Boolean(lockedCustomer)
  const initial = useMemo(() => ({
    customerId: lockedCustomer?.id ?? request?.customerId ?? initialCustomerId ?? '',
    pendingName: pendingCustomerName?.trim() ?? '',
    body: request?.body ?? '',
    sourceUrl: request?.sourceUrl ?? initialSourceUrl ?? '',
  }), []) // eslint-disable-line react-hooks/exhaustive-deps
  const [customerId, setCustomerId] = useState(initial.customerId)
  const [pendingName, setPendingName] = useState(initial.pendingName)
  const [body, setBody] = useState(initial.body)
  const [sourceUrl, setSourceUrl] = useState(initial.sourceUrl)
  const [files, setFiles] = useState<File[]>([])
  const [saving, setSaving] = useState(false)
  const bodyRef = useRef<HTMLTextAreaElement>(null)
  const fileRef = useRef<HTMLInputElement>(null)
  const mac = isMacPlatform()

  const selected = lockedCustomer ?? data.customers.find(item => item.id === customerId)
  const hasChanges = body.trim() !== initial.body.trim() || sourceUrl !== initial.sourceUrl || files.length > 0 || (!hideCustomer && (customerId !== initial.customerId || pendingName !== initial.pendingName))

  // Linear focuses the editor with the caret at the end.
  useEffect(() => {
    const field = bodyRef.current
    if (!field) return
    field.focus({ preventScroll: false })
    field.setSelectionRange(field.value.length, field.value.length)
  }, [])
  useLayoutEffect(() => {
    const field = bodyRef.current
    if (!field) return
    field.style.height = '0px'
    field.style.height = `${Math.min(300, Math.max(72, field.scrollHeight))}px`
  }, [body])

  const discard = async () => {
    if (!onCancel) return
    if (hasChanges && !await confirmAction(t('Discard this request?'), { description: t('Confirm that you want to discard this customer request.'), confirmLabel: t('Discard'), danger: true })) return
    onCancel()
  }

  const submit = async (event?: FormEvent) => {
    event?.preventDefault()
    if (saving) return
    const text = body.trim()
    const customerChosen = Boolean(selected || pendingName)
    if (!customerChosen && !text && !sourceUrl && !files.length) {
      toast.error(t('Please select a customer, provide a request, or specify a source.'))
      return
    }
    setSaving(true)
    try {
      let saved: CustomerRequest
      if (request) {
        saved = await updateCustomerRequest(request.id, { body: text, sourceUrl })
      } else {
        saved = await createCustomerRequest({
          customerId: selected?.id,
          customerName: selected ? undefined : pendingName || undefined,
          body: text,
          source: 'manual',
          sourceUrl: sourceUrl || undefined,
          issueId,
          projectId: issueId ? undefined : projectId,
        })
      }
      if (files.length) {
        const attachments = []
        for (const file of files) attachments.push(await uploadCustomerRequestAttachment(saved.id, file))
        saved = { ...saved, attachments: [...(saved.attachments ?? []), ...attachments] }
      }
      toast.success(t(editing ? 'Customer request updated' : 'Customer request added'))
      if (editing) await onSaved?.(saved)
      else await onCreated?.(saved)
    } catch {
      toast.error(t('Failed to save customer request'))
    } finally {
      setSaving(false)
    }
  }

  const onKeyDown = (event: KeyboardEvent<HTMLFormElement>) => {
    // Keys from portaled menus bubble here through React; only handle the form's own fields.
    if (event.defaultPrevented || !event.currentTarget.contains(event.target as Node)) return
    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) { event.preventDefault(); void submit() }
    else if (event.key === 'Escape' && onCancel) { event.preventDefault(); event.stopPropagation(); void discard() }
    else if (event.key.toLowerCase() === 'u' && event.shiftKey && (mac ? event.metaKey : event.ctrlKey)) { event.preventDefault(); fileRef.current?.click() }
  }

  return (
    <form className={`embedded-customer-need-form is-${variant}${className ? ` ${className}` : ''}`} data-host={host} onKeyDown={onKeyDown} onSubmit={event => void submit(event)}>
      <div className="embedded-customer-need-form__surface">
        {!hideCustomer && (
          <CustomerButton
            customers={data.customers}
            selected={selected}
            pendingName={pendingName}
            onPick={(id, name) => {
              setCustomerId(id ?? '')
              setPendingName(name ?? '')
              requestAnimationFrame(() => bodyRef.current?.focus())
            }}
          />
        )}
        <textarea
          ref={bodyRef}
          aria-label={t('Request')}
          className="embedded-customer-need-form__body"
          data-customer-hidden={hideCustomer || undefined}
          placeholder={t('Add request details')}
          value={body}
          onChange={event => setBody(event.target.value)}
        />
        {files.length > 0 && (
          <div className="embedded-customer-need-form__files">
            {files.map((file, index) => (
              <span className="embedded-customer-need-form__file" key={`${file.name}-${index}`} data-i18n-ignore>
                <AttachIcon size={12}/><span>{file.name}</span>
                <button type="button" aria-label={t('Remove attachment')} onClick={() => setFiles(current => current.filter((_, item) => item !== index))}><ClearIcon size={10}/></button>
              </span>
            ))}
          </div>
        )}
        <div className="embedded-customer-need-form__footer">
          {editing && request?.source && request.source !== 'manual' ? <span/> : <SourceButton value={sourceUrl} onChange={setSourceUrl}/>}
          <div className="embedded-customer-need-form__actions">
            <FlowTooltip label={t('Attach images, files, or videos')} shortcut={mac ? '⌘⇧U' : 'Ctrl Shift U'}>
              <button className="embedded-customer-need-form__icon-button" type="button" aria-label={t('Attach images, files, or videos')} onClick={() => fileRef.current?.click()}><AttachIcon size={14}/></button>
            </FlowTooltip>
            <input ref={fileRef} hidden multiple type="file" onChange={event => { const picked = Array.from(event.target.files ?? []); if (picked.length) setFiles(current => [...current, ...picked]); event.target.value = '' }}/>
            {onCancel && (
              <FlowTooltip label={t('Discard')} shortcut="Esc">
                <button className="embedded-customer-need-form__cancel" type="button" aria-label={t('Discard')} onClick={() => void discard()}>{t('Cancel')}</button>
              </FlowTooltip>
            )}
            <FlowTooltip label={<>{t('Press')} <kbd>{mac ? '⌘' : 'Ctrl'}</kbd><kbd>↵</kbd> {t(editing ? 'to save request' : 'to create request')}</>}>
              <button className="embedded-customer-need-form__create" disabled={saving} type="submit">{t(editing ? 'Save' : 'Create')}</button>
            </FlowTooltip>
          </div>
        </div>
      </div>
    </form>
  )
}

/**
 * The composer's customer button (Linear's `customer-select-button`): the customer's logo and
 * name — or the pending name, or "Customer" — opening a "Search customers…" menu. Picking the
 * current customer again clears it.
 */
function CustomerButton({ customers, selected, pendingName, onPick }: { customers: readonly Customer[]; selected?: Customer; pendingName: string; onPick: (customerId?: string, pendingName?: string) => void }) {
  const { t } = useI18n()
  const label = selected?.name ?? (pendingName || t('Customer'))
  const options: LinearMenuOption[] = [
    ...(pendingName ? [{ id: PENDING_ID, label: pendingName, translate: false, icon: <CustomerLogo customer={null} size={16}/>, detail: t('New customer') }] : []),
    ...[...customers].sort((left, right) => left.name.localeCompare(right.name)).map(customer => ({ id: customer.id, label: customer.name, translate: false, keywords: customer.domains?.join(' '), icon: <CustomerLogo customer={customer} size={16}/> })),
  ]
  const selectedIds = new Set(selected ? [selected.id] : pendingName ? [PENDING_ID] : [])
  return <DropdownMenu.Root>
    <DropdownMenu.Trigger asChild>
      <button className="embedded-customer-need-form__customer" id="customer-select-button" type="button" aria-label={t('Search customers')}>
        <CustomerLogo customer={selected ?? null} size={14}/>
        <span data-i18n-ignore={selected || pendingName ? true : undefined}>{label}</span>
      </button>
    </DropdownMenu.Trigger>
    <LinearDropdownMenuContent label={t('Search customers…')} className="embedded-customer-need-menu">
      <LinearMenuOptions
        options={options}
        selected={selectedIds}
        placeholder="Search customers…"
        emptyLabel={customers.length ? 'No results' : 'Type a name to create your first customer'}
        onChoose={id => {
          if (id === PENDING_ID) onPick(undefined, pendingName)
          else onPick(id === selected?.id ? undefined : id)
        }}
        footer={query => query && !looksLikeUrl(query) && query.toLocaleLowerCase() !== pendingName.toLocaleLowerCase()
          ? <LinearMenuItem icon={<RequestPlusIcon/>} label={t('Create new customer: "{name}"').replace('{name}', query)} translate={false} onSelect={() => onPick(undefined, query)}/>
          : null}
      />
    </LinearDropdownMenuContent>
  </DropdownMenu.Root>
}

const PENDING_ID = '__pending-customer'

/** "Source" button with Linear's link popover: "Paste link…", Enter saves, the × clears. */
function SourceButton({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState(value)
  const host = value ? sourceHost(value) : undefined
  const save = () => {
    const url = normalizeSourceUrl(draft)
    if (url === undefined) { toast.error(t('Invalid URL, please enter a valid URL')); return }
    onChange(url)
    setOpen(false)
  }
  return <Popover.Root open={open} onOpenChange={next => { if (next) { setDraft(value); setOpen(true) } else save() }}>
    <FlowTooltip label={value || t('Add source')} disabled={open}>
      <Popover.Trigger asChild>
        <button className="embedded-customer-need-form__source" type="button" aria-haspopup="dialog" aria-label={t('Add source')}>
          <SourceLinkIcon size={14}/><span data-i18n-ignore={host ? true : undefined}>{host ? `via ${host}` : t('Source')}</span>
        </button>
      </Popover.Trigger>
    </FlowTooltip>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating" className="embedded-customer-need-source-popover" side="bottom" align="start" sideOffset={4} collisionPadding={10}
        onEscapeKeyDown={event => { event.preventDefault(); setOpen(false) }}
        onKeyDown={event => event.stopPropagation()}>
        <input
          autoFocus
          aria-label={t('Source URL')}
          placeholder={t('Paste link…')}
          value={draft}
          onChange={event => setDraft(event.target.value)}
          onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); save() } }}
        />
        <span className="embedded-customer-need-source-popover__divider" aria-hidden="true"/>
        <FlowTooltip label={t('Remove link')}>
          <button type="button" aria-label={t('Clear source')} onClick={() => { setDraft(''); onChange(''); setOpen(false) }}><ClearIcon size={12}/></button>
        </FlowTooltip>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

/** Map Important ↔ REST priority (1 = important). */
export function importantFromPriority(priority?: number) {
  return (priority ?? 0) >= IMPORTANT_PRIORITY
}

export function priorityFromImportant(important: boolean) {
  return important ? IMPORTANT_PRIORITY : undefined
}
