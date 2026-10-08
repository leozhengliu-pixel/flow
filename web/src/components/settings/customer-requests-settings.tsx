import { useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from 'react'
import { toast } from 'sonner'

import { CustomerStatusIcon } from '@/components/customer/customer-status-icon'
import { TeamIcon } from '@/components/issue/issue-icons'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { AppLink } from '@/components/ui/app-link'
import { FlowTooltip, TooltipProvider } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import { customersPath } from '@/lib/app-routes'
import {
  archiveCustomerTaxonomyItem, createCustomerTaxonomyItem, customerCurrencyOptions, customerSettingsErrorMessage,
  customerSourceError, restoreCustomerTaxonomyItem, updateCustomerTaxonomyItem,
  CUSTOMER_REVENUE_FORMATS, CUSTOMER_TAXONOMY_MAX_DESCRIPTION_LENGTH, CUSTOMER_TAXONOMY_MAX_NAME_LENGTH,
  DEFAULT_NEW_CUSTOMER_STATUS_COLOR, DEFAULT_NEW_CUSTOMER_TIER_COLOR,
  type CustomerTaxonomyItem, type CustomerTaxonomyKind,
} from '@/lib/customer-settings'
import type { BootstrapData, FeatureSettings, WorkspaceSettings } from '@/types/flow'

import { StatusColorPicker, StatusMenuIcon } from './issues-projects-settings'
import { SettingsSelect, SettingsToggle } from './settings-primitives'
import './customer-requests-settings.css'

const DOCS_URL = 'https://flow.app/docs/customer-requests'
const GENERIC_DOMAINS_DOCS_URL = `${DOCS_URL}#generic-domains`
const NO_DEFAULT_TEAM = 'none'

type SetFeature = <K extends keyof FeatureSettings>(key: K, value: FeatureSettings[K]) => void | Promise<void>
type Props = {
  data: BootstrapData
  settings: WorkspaceSettings
  busy: boolean
  setEnabled: (id: string, value: boolean) => void
  setFeature: SetFeature
  onReload: () => Promise<void>
}

/** Settings › Customer requests. */
export function CustomerRequestsSettings({ data, settings, busy, setEnabled, setFeature, onReload }: Props) {
  const { t } = useI18n()
  const fs = settings.featureSettings
  const enabled = settings.featureFlags['customer-requests'] ?? true
  const canToggle = ['admin', 'owner'].includes(data.viewerRole)
  const canManage = ['admin', 'owner', 'member'].includes(data.viewerRole)
  const disabled = !enabled
  const locked = disabled || !canManage
  const saving = locked || busy
  const customerCount = data.customers.length
  return <div className="feature-settings crs-page">
    <header className="crs-header">
      <h1>{t('Customer requests')}</h1>
      <p>{t('Associate customers with projects and issues to align development efforts with real user needs. Manage and track customer requests across your entire organization.')} <DocsLink href={DOCS_URL}>{t('Docs')}</DocsLink></p>
    </header>
    <section className="crs-card">
      <div className="crs-row">
        <div className="crs-row-copy"><strong>{t('Enable Customer requests')}</strong><span>{t('Workspace-wide access to create and view customer requests')}</span></div>
        <SettingsToggle checked={enabled} disabled={busy || !canToggle} label={t('Enable Customer requests')} onChange={value => setEnabled('customer-requests', value)}/>
      </div>
    </section>
    <section className={`crs-card crs-link-card${disabled ? ' is-disabled' : ''}`}>
      <div className="crs-row crs-link-row">
        <AppLink aria-label={t('Manage customers settings')} className="crs-row-link" href={customersPath(data.workspace.urlKey)} aria-disabled={disabled || undefined} tabIndex={disabled ? -1 : undefined} onClick={event => { if (disabled) event.preventDefault() }}/>
        <span className="crs-row-icon"><CustomerDefaultLogoIcon/></span>
        <div className="crs-row-copy"><strong>{t('Manage customers')}</strong><span>{t('Manage your list of customers and their requests')}</span></div>
        <span className="crs-row-detail">{customerCount === 0 ? t('No customers') : countText(t, customerCount, 'customer', 'customers')}</span>
        <ChevronRightIcon/>
      </div>
    </section>

    <CustomerSection disabled={disabled} title="Issue routing" description="When a new issue is created from a customer page, it will be routed to the default team’s triage or backlog. This centralizes customer requests for ease of management and prioritization.">
      <section className="crs-card">
        <div className="crs-row">
          <div className="crs-row-copy"><strong>{t('Default team for customer requests')}</strong></div>
          <DefaultTeamSelect data={data} value={fs.customerDefaultTeamId} disabled={saving} onChange={value => setFeature('customerDefaultTeamId', value === NO_DEFAULT_TEAM ? '' : value)}/>
        </div>
      </section>
    </CustomerSection>

    <CustomerSection disabled={disabled} title="Customer statuses" description="Define statuses for segmenting customers">
      <TaxonomyList kind="status" items={data.customerStatuses ?? []} disabled={locked} onReload={onReload}/>
    </CustomerSection>
    <CustomerSection disabled={disabled} title="Customer tiers" description="Define tiers for segmenting customers">
      <TaxonomyList kind="tier" items={data.customerTiers ?? []} disabled={locked} onReload={onReload}/>
    </CustomerSection>

    <CustomerSection disabled={disabled} title="Display options">
      <section className="crs-card">
        <div className="crs-row">
          <div className="crs-row-copy"><strong>{t('Revenue formatting')}</strong><span>{t('Data imports must be in annual figures, but can be displayed as monthly or annual')}</span></div>
          <SettingsSelect label={t('Revenue formatting')} value={fs.customerRevenueFormat || 'annual'} disabled={saving} menuClassName="settings-select-menu crs-select-menu" options={CUSTOMER_REVENUE_FORMATS.map(item => ({ value: item.value, label: t(item.label) }))} onChange={value => setFeature('customerRevenueFormat', value as FeatureSettings['customerRevenueFormat'])}/>
        </div>
        <div className="crs-row">
          <div className="crs-row-copy"><strong>{t('Revenue currency')}</strong><span>{t('The currency used when displaying customer revenue')}</span></div>
          <CurrencySelect value={fs.customerRevenueCurrency || 'USD'} disabled={saving} onChange={value => setFeature('customerRevenueCurrency', value)}/>
        </div>
      </section>
    </CustomerSection>

    <CustomerSection disabled={disabled} title="Customer attributes data source" description="Sync customer attributes from an external data source">
      <section className="crs-card">
        <div className="crs-row">
          <div className="crs-row-copy"><strong>{t('External data source')}</strong></div>
          <TooltipProvider delayDuration={0} skipDelayDuration={0}>
            <FlowTooltip label={t('Configure a supported integration to sync customer attributes from an external data source')} contentClassName="flow-tooltip-content--title">
              <span className="crs-muted-value" tabIndex={0}>{t('None')}</span>
            </FlowTooltip>
          </TooltipProvider>
        </div>
        <div className="crs-row">
          <div className="crs-row-copy"><strong>{t('Enable manual edits')}</strong><span>{t('Attributes can be edited in the Flow UI')}</span></div>
          <SettingsToggle checked={fs.customerManualEdits} disabled={saving} label={t('Enable manual edits')} onChange={value => setFeature('customerManualEdits', value)}/>
        </div>
      </section>
    </CustomerSection>

    <CustomerSection disabled={disabled} subsection title="Excluded domains and emails" description="Domains and emails that should never create customer requests">
      <SourceList kind="excluded" values={fs.customerExcludedDomains ?? []} disabled={locked} busy={busy} onChange={async values => { await setFeature('customerExcludedDomains', values); await onReload() }}/>
    </CustomerSection>
    <CustomerSection disabled={disabled} subsection title="Generic domains and emails" description={<>{t('Domains and emails that are not associated with a specific customer. Common providers like Gmail, Outlook, etc. are already included.')} <DocsLink href={GENERIC_DOMAINS_DOCS_URL}>{t('List of generic domains')}</DocsLink></>}>
      <SourceList kind="generic" values={fs.customerGenericDomains ?? []} disabled={locked} busy={busy} onChange={values => setFeature('customerGenericDomains', values)}/>
    </CustomerSection>
  </div>
}

function CustomerSection({ title, description, disabled, subsection, children }: { title: string; description?: ReactNode; disabled?: boolean; subsection?: boolean; children: ReactNode }) {
  const { t } = useI18n()
  return <section className={`crs-section${subsection ? ' is-subsection' : ''}${disabled ? ' is-disabled' : ''}`} aria-disabled={disabled || undefined}>
    <header>
      {subsection ? <span className="crs-subsection-title">{t(title)}</span> : <h3>{t(title)}</h3>}
      {description && <div className="crs-section-description">{typeof description === 'string' ? t(description) : description}</div>}
    </header>
    {children}
  </section>
}

function DocsLink({ href, children }: { href: string; children: ReactNode }) {
  return <a className="crs-docs-link" href={href} rel="noopener noreferrer" target="_blank">{children}</a>
}

function countText(t: (value: string) => string, count: number, singular: string, plural: string) {
  const formatted = new Intl.NumberFormat().format(count)
  return count === 1 ? t(`1 ${singular}`) : t(`{count} ${plural}`).replace('{count}', formatted)
}

function DefaultTeamSelect({ data, value, disabled, onChange }: { data: BootstrapData; value?: string; disabled: boolean; onChange: (value: string) => void }) {
  const { t } = useI18n()
  const current = value ? data.teams.find(team => team.id === value) : undefined
  const active = data.teams.filter(team => !team.retiredAt && !team.archivedAt)
  const options = [
    { value: NO_DEFAULT_TEAM, label: t('No default team'), icon: <span className="crs-option-icon"><NoTeamIcon/></span> },
    ...(current && (current.retiredAt || current.archivedAt) ? [{ value: current.id, label: `${current.name} ${t('(retired)')}`, entityName: true, disabled: true, icon: <span className="crs-option-icon is-team"><TeamIcon team={current} size={14}/></span> }] : []),
    ...active.map(team => ({ value: team.id, label: team.name, entityName: true, icon: <span className="crs-option-icon is-team"><TeamIcon team={team} size={14}/></span> })),
  ]
  // A default team that no longer exists shows the "Select a team" placeholder.
  const selected = current ? current.id : value ? '' : NO_DEFAULT_TEAM
  return <SettingsSelect label={t('Default team for customer requests')} value={selected} placeholder={t('Select a team')} disabled={disabled} menuClassName="settings-select-menu crs-select-menu" options={options} onChange={onChange}/>
}

function CurrencySelect({ value, disabled, onChange }: { value: string; disabled: boolean; onChange: (value: string) => void }) {
  const { t } = useI18n()
  const options = useMemo(() => customerCurrencyOptions().map(option => ({ ...option, entityName: true })), [])
  return <SettingsSelect label={t('Revenue currency')} value={value} disabled={disabled} menuClassName="settings-select-menu crs-select-menu crs-currency-menu" options={options} onChange={onChange}/>
}

type Draft = { name: string; description: string; color?: string }

function TaxonomyList({ kind, items, disabled, onReload }: { kind: CustomerTaxonomyKind; items: CustomerTaxonomyItem[]; disabled: boolean; onReload: () => Promise<void> }) {
  const { t } = useI18n()
  const [local, setLocal] = useState(items)
  const [editing, setEditing] = useState<string | 'new' | null>(null)
  const [dragging, setDragging] = useState<string | null>(null)
  useEffect(() => setLocal(items), [items])
  const visible = useMemo(() => local.filter(item => !item.archivedAt).sort((a, b) => (a.position ?? 0) - (b.position ?? 0)), [local])
  const label = kind === 'status' ? 'customer status' : 'customer tier'
  const count = visible.length
  const title = kind === 'status'
    ? countText(t, count, 'customer status', 'customer statuses')
    : count ? countText(t, count, 'customer tier', 'customer tiers') : null
  useEffect(() => { if (disabled) setEditing(null) }, [disabled])

  const run = async (action: () => Promise<unknown>, rollback?: () => void) => {
    try {
      await action()
      await onReload()
      return true
    } catch (error) {
      rollback?.()
      toast.error(customerSettingsErrorMessage(error))
      return false
    }
  }
  const save = async (item: CustomerTaxonomyItem | undefined, draft: Draft) => {
    const input = { name: draft.name.trim(), description: draft.description.trim(), ...(kind === 'status' && draft.color ? { color: draft.color } : {}) }
    const before = local
    if (item) {
      setLocal(current => current.map(value => value.id === item.id ? { ...value, ...input } : value))
      setEditing(null)
      await run(() => updateCustomerTaxonomyItem(kind, item.id, input), () => setLocal(before))
      return
    }
    const last = visible.at(-1)
    const position = last ? (last.position ?? 0) + 1 : 0
    const created = await run(async () => {
      const result = await createCustomerTaxonomyItem(kind, { ...input, position })
      setLocal(current => [...current, result])
    })
    if (created) setEditing(null)
  }
  const remove = async (item: CustomerTaxonomyItem) => {
    const before = local
    setLocal(current => current.filter(value => value.id !== item.id))
    await run(async () => {
      const result = await archiveCustomerTaxonomyItem(kind, item.id)
      const reassigned = result.reassignedCustomerIds ?? []
      toast(t(kind === 'status' ? 'Customer status deleted' : 'Customer tier deleted'), {
        action: {
          label: t('Undo delete {name}').replace('{name}', item.name),
          onClick: () => {
            setLocal(current => current.some(value => value.id === item.id) ? current : [...current, item])
            void run(() => restoreCustomerTaxonomyItem(kind, item.id, reassigned))
          },
        },
      })
    }, () => setLocal(before))
  }
  const moveTo = (sourceId: string, targetId: string) => {
    if (sourceId === targetId) return
    const order = visible.filter(item => item.id !== sourceId)
    const source = visible.find(item => item.id === sourceId)
    const targetIndex = order.findIndex(item => item.id === targetId)
    if (!source || targetIndex < 0) return
    const sourceIndex = visible.findIndex(item => item.id === sourceId)
    const insertAt = sourceIndex <= visible.findIndex(item => item.id === targetId) ? targetIndex + 1 : targetIndex
    order.splice(insertAt, 0, source)
    const previous = order[insertAt - 1], next = order[insertAt + 1]
    const position = previous && next ? ((previous.position ?? 0) + (next.position ?? 0)) / 2 : previous ? (previous.position ?? 0) + 1 : next ? (next.position ?? 0) - 1 : 0
    const before = local
    setLocal(current => current.map(item => item.id === sourceId ? { ...item, position } : item))
    void run(() => updateCustomerTaxonomyItem(kind, sourceId, { position }), () => setLocal(before))
  }

  return <section className="crs-card crs-list-card">
    <div className="crs-list-header">
      {title ? <span className="crs-list-title">{title}</span> : <span className="crs-list-empty">{t('No customer tiers')}</span>}
      <button type="button" className="crs-icon-button" aria-label={t(kind === 'status' ? 'Create new customer status' : 'Create new customer tier')} disabled={disabled} onClick={() => setEditing('new')}><PlusIcon/></button>
    </div>
    <div className="crs-list-body">
      {visible.map(item => editing === item.id && !disabled
        ? <TaxonomyEditor key={item.id} kind={kind} label={label} item={item} onCancel={() => setEditing(null)} onSubmit={draft => save(item, draft)}/>
        : <TaxonomyRow key={item.id} kind={kind} item={item} disabled={disabled} dragging={dragging === item.id}
            onDragStart={() => setDragging(item.id)} onDragEnd={() => setDragging(null)}
            onDrop={() => { if (dragging) moveTo(dragging, item.id); setDragging(null) }}
            onMove={delta => { const index = visible.findIndex(value => value.id === item.id); const target = visible[index + delta]; if (target) moveTo(item.id, target.id) }}
            onEdit={() => setEditing(item.id)} onDelete={() => void remove(item)}/>)}
      {editing === 'new' && !disabled && <TaxonomyEditor kind={kind} label={label} onCancel={() => setEditing(null)} onSubmit={draft => save(undefined, draft)}/>}
    </div>
  </section>
}

function TaxonomyRow({ kind, item, disabled, dragging, onDragStart, onDragEnd, onDrop, onMove, onEdit, onDelete }: { kind: CustomerTaxonomyKind; item: CustomerTaxonomyItem; disabled: boolean; dragging: boolean; onDragStart: () => void; onDragEnd: () => void; onDrop: () => void; onMove: (delta: number) => void; onEdit: () => void; onDelete: () => void }) {
  const { t } = useI18n()
  const [menuOpen, setMenuOpen] = useState(false)
  return <div className={`crs-option-row${dragging ? ' is-dragging' : ''}${menuOpen ? ' is-menu-open' : ''}`} role="button" tabIndex={disabled ? -1 : 0} aria-disabled={disabled} aria-roledescription="sortable"
    draggable={!disabled}
    onDragStart={event => { event.dataTransfer.effectAllowed = 'move'; onDragStart() }}
    onDragEnd={onDragEnd}
    onDragOver={event => { if (!disabled) event.preventDefault() }}
    onDrop={event => { event.preventDefault(); onDrop() }}
    onKeyDown={event => {
      if (disabled || !event.altKey) return
      if (event.key === 'ArrowUp') { event.preventDefault(); onMove(-1) }
      if (event.key === 'ArrowDown') { event.preventDefault(); onMove(1) }
    }}>
    {!disabled && <span className="crs-drag-handle" aria-hidden="true"><svg width="6" height="10" viewBox="0 0 6 10"><path fillRule="evenodd" d="M1 8a1 1 0 1 1 0 2 1 1 0 0 1 0-2Zm4 0a1 1 0 1 1 0 2 1 1 0 0 1 0-2ZM1 4a1 1 0 1 1 0 2 1 1 0 0 1 0-2Zm4 0a1 1 0 1 1 0 2 1 1 0 0 1 0-2ZM1 0a1 1 0 1 1 0 2 1 1 0 0 1 0-2Zm4 0a1 1 0 1 1 0 2 1 1 0 0 1 0-2Z"/></svg></span>}
    <div className="crs-option-content">
      {kind === 'status' && <span className="crs-option-swatch"><CustomerStatusIcon color={item.color}/></span>}
      <span className="crs-option-name" data-i18n-ignore>{item.name}</span>
      {item.description && <span className="crs-option-description" data-i18n-ignore>{item.description}</span>}
    </div>
    {!disabled && <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
      <DropdownMenuTrigger asChild><button type="button" className="crs-icon-button crs-row-menu-button" aria-label={t('Open menu')}><MoreIcon/></button></DropdownMenuTrigger>
      <DropdownMenuContent align="end" sideOffset={4} className="domain-label-row-menu crs-row-menu">
        <DropdownMenuItem onSelect={onEdit}><span className="crs-menu-icon"><StatusMenuIcon name="edit"/></span><span>{t('Edit')}</span></DropdownMenuItem>
        <DropdownMenuItem onSelect={onDelete}><span className="crs-menu-icon"><StatusMenuIcon name="delete"/></span><span>{t('Delete')}</span></DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>}
  </div>
}

function TaxonomyEditor({ kind, label, item, onCancel, onSubmit }: { kind: CustomerTaxonomyKind; label: string; item?: CustomerTaxonomyItem; onCancel: () => void; onSubmit: (draft: Draft) => Promise<void> | void }) {
  const { t } = useI18n()
  const nameRef = useRef<HTMLInputElement>(null)
  const [draft, setDraft] = useState<Draft>({ name: item?.name ?? '', description: item?.description ?? '', color: kind === 'status' ? (item?.color || DEFAULT_NEW_CUSTOMER_STATUS_COLOR) : (item?.color || DEFAULT_NEW_CUSTOMER_TIER_COLOR) })
  const [saving, setSaving] = useState(false)
  useEffect(() => { nameRef.current?.focus() }, [])
  const submit = async (event?: FormEvent) => {
    event?.preventDefault()
    const errors = [
      !draft.name.trim() ? `The ${label} name cannot be empty.` : draft.name.trim().length > CUSTOMER_TAXONOMY_MAX_NAME_LENGTH ? 'Name is too long.' : '',
      draft.description.length > CUSTOMER_TAXONOMY_MAX_DESCRIPTION_LENGTH ? 'Description is too long.' : '',
    ].filter(Boolean)
    if (errors.length) {
      toast.error(t('Name required'), { description: errors.map(error => t(error)).join(', ') })
      nameRef.current?.focus()
      return
    }
    setSaving(true)
    try { await onSubmit(draft) } finally { setSaving(false) }
  }
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); onCancel() }
  }
  return <form className="crs-option-editor" onSubmit={submit} onKeyDown={onKeyDown}>
    <div className="crs-option-editor-fields">
      {kind === 'status' && <StatusColorPicker color={draft.color ?? DEFAULT_NEW_CUSTOMER_STATUS_COLOR} type="status" preview={<CustomerStatusIcon color={draft.color}/>} onChange={color => setDraft(current => ({ ...current, color }))}/>}
      <input ref={nameRef} autoComplete="off" data-1p-ignore aria-label={t('Name')} placeholder={t('Name')} maxLength={CUSTOMER_TAXONOMY_MAX_NAME_LENGTH} value={draft.name} onChange={event => setDraft(current => ({ ...current, name: event.target.value }))}/>
      <input autoComplete="off" data-1p-ignore aria-label={t('Description')} placeholder={t('Description…')} maxLength={CUSTOMER_TAXONOMY_MAX_DESCRIPTION_LENGTH} value={draft.description} onChange={event => setDraft(current => ({ ...current, description: event.target.value }))}/>
    </div>
    <div className="crs-editor-actions">
      <button type="button" className="crs-button" aria-label={t('Cancel')} onClick={onCancel}>{t('Cancel')}</button>
      <button type="submit" className="crs-button is-primary" aria-label={t('Submit')} disabled={saving}>{t(item ? 'Save' : 'Create')}</button>
    </div>
  </form>
}

function SourceList({ kind, values, disabled, busy, onChange }: { kind: 'excluded' | 'generic'; values: string[]; disabled: boolean; busy: boolean; onChange: (values: string[]) => void | Promise<void> }) {
  const { t } = useI18n()
  const [adding, setAdding] = useState(false)
  useEffect(() => { if (disabled) setAdding(false) }, [disabled])
  const allEmails = values.every(value => value.includes('@'))
  const allDomains = values.every(value => !value.includes('@'))
  const title = !values.length ? null
    : kind === 'excluded' ? countText(t, values.length, 'exclusion', 'exclusions')
    : allEmails ? countText(t, values.length, 'email', 'emails')
    : allDomains ? countText(t, values.length, 'domain', 'domains')
    : t('{count} domains and emails').replace('{count}', String(values.length))
  const add = async (source: string) => {
    if (kind === 'excluded') {
      const confirmed = await confirmAction(t('Add {source} to the list of excluded domains and emails?').replace('{source}', source), {
        description: t(source.includes('@') ? 'Requests will not be created from this email, and any existing requests will be archived.' : 'Requests will not be created from this domain, and any existing requests will be archived.'),
        confirmLabel: t('Add'),
        danger: false,
      })
      setAdding(false)
      if (!confirmed) return
    } else setAdding(false)
    await onChange([...values, source])
  }
  const remove = async (value: string) => {
    if (kind === 'excluded') {
      const confirmed = await confirmAction(t('Remove {value} from the list of excluded domains and emails?').replace('{value}', value), {
        description: t(value.includes('@') ? 'Requests from this email will be converted to customer requests, including any that were previously excluded.' : 'Requests from this domain will be converted to customer requests, including any that were previously excluded.'),
        confirmLabel: t('Remove'),
        danger: false,
      })
      if (!confirmed) return
    }
    await onChange(values.filter(item => item !== value))
  }
  return <section className="crs-card crs-source-card">
    <div className="crs-list-header">
      <h3 className="crs-source-title">{title ? <span className="crs-list-title">{title}</span> : <span className="crs-list-empty">{t(kind === 'excluded' ? 'No excluded domains and emails' : 'No custom generic domains and emails')}</span>}</h3>
      <button type="button" className="crs-icon-button" aria-label={t('Open menu')} aria-haspopup="menu" disabled={disabled || busy} onClick={() => setAdding(true)}><PlusIcon/></button>
    </div>
    {values.map(value => <div className="crs-row crs-source-row" key={value}>
      <div className="crs-row-copy"><strong data-i18n-ignore>{value}</strong></div>
      <DropdownMenu>
        <DropdownMenuTrigger asChild><button type="button" className="crs-icon-button" aria-label={t('Open menu')} disabled={disabled || busy}><MoreIcon/></button></DropdownMenuTrigger>
        <DropdownMenuContent align="end" sideOffset={4} className="domain-label-row-menu crs-row-menu">
          <DropdownMenuItem onSelect={() => void remove(value)}><span>{t('Remove')}</span></DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>)}
    {adding && !disabled && <SourceForm values={values} onCancel={() => setAdding(false)} onSubmit={source => void add(source)}/>}
  </section>
}

function SourceForm({ values, onCancel, onSubmit }: { values: string[]; onCancel: () => void; onSubmit: (source: string) => void }) {
  const { t } = useI18n()
  const [source, setSource] = useState('')
  const [error, setError] = useState<string>()
  const inputRef = useRef<HTMLInputElement>(null)
  useEffect(() => { inputRef.current?.focus() }, [])
  return <form className="crs-row crs-source-form" onSubmit={event => {
    event.preventDefault()
    const problem = customerSourceError(source, values)
    setError(problem)
    if (!problem) onSubmit(source.trim().toLowerCase())
  }}>
    <div className="crs-source-field">
      <input ref={inputRef} autoComplete="off" data-1p-ignore aria-label={t('Domain or email address')} placeholder={t('Domain or email address')} maxLength={100} value={source} aria-invalid={error ? true : undefined}
        onChange={event => setSource(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') { event.preventDefault(); onCancel() } }}/>
      {error && <span className="crs-field-error" role="alert">{t(error)}</span>}
    </div>
    <div className="crs-editor-actions">
      <button type="button" className="crs-button" aria-label={t('Cancel')} onClick={onCancel}>{t('Cancel')}</button>
      <button type="submit" className="crs-button is-primary" aria-label={t('Submit')}>{t('Add')}</button>
    </div>
  </form>
}

/* Icons copied from the reference app's inline SVGs. */
function CustomerDefaultLogoIcon() {
  return <svg aria-hidden="true" width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
    <path fillRule="evenodd" clipRule="evenodd" d="M11.0247 12.3333C13.6728 12.3334 14.6225 13.529 14.9606 14.319C15.112 14.6739 14.806 15 14.4046 15H7.59537C7.18784 14.9997 6.8816 14.6641 7.04464 14.3073C7.40663 13.5172 8.38955 12.3333 11.0247 12.3333Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M11 7C12.1045 7 12.9998 7.89543 12.9998 9C12.9998 10.1046 12.1045 11 11 11C9.89553 11 9.00018 10.1046 9.00018 9C9.00018 7.89543 9.89553 7 11 7Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M10 4.25V3.75C10 3.05964 9.44036 2.5 8.75 2.5H3.75C3.05964 2.5 2.5 3.05964 2.5 3.75V13.25C2.5 13.3881 2.61193 13.5 2.75 13.5H4.25C4.66421 13.5 5 13.8358 5 14.25C5 14.6642 4.66421 15 4.25 15H2.75C1.7835 15 1 14.2165 1 13.25V3.75C1 2.23122 2.23122 1 3.75 1H8.75C10.2688 1 11.5 2.23122 11.5 3.75V4.25C11.5 4.66421 11.1642 5 10.75 5C10.3358 5 10 4.66421 10 4.25Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M7.75 4.25C8.16421 4.25 8.5 4.58579 8.5 5C8.5 5.41421 8.16421 5.75 7.75 5.75H4.75C4.33579 5.75 4 5.41421 4 5C4 4.58579 4.33579 4.25 4.75 4.25H7.75Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M6.5 7.25C6.91421 7.25 7.25 7.58579 7.25 8C7.25 8.41421 6.91421 8.75 6.5 8.75H4.75C4.33579 8.75 4 8.41421 4 8C4 7.58579 4.33579 7.25 4.75 7.25H6.5Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M6.5 10.25C6.91421 10.25 7.25 10.5858 7.25 11C7.25 11.4142 6.91421 11.75 6.5 11.75H4.75C4.33579 11.75 4 11.4142 4 11C4 10.5858 4.33579 10.25 4.75 10.25H6.5Z"/>
  </svg>
}
function NoTeamIcon() {
  return <svg aria-hidden="true" width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
    <path fillRule="evenodd" clipRule="evenodd" d="M10.25 6.75C10.25 7.99264 9.24264 9 8 9C6.75736 9 5.75 7.99264 5.75 6.75C5.75 5.50736 6.75736 4.5 8 4.5C9.24264 4.5 10.25 5.50736 10.25 6.75Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M8.5752 10C9.97242 10 11.2611 10.6106 12.1436 11.6143C12.1563 11.5997 12.17 11.586 12.1826 11.5713C12.4518 11.2567 12.9255 11.2202 13.2402 11.4893C13.5548 11.7585 13.5913 12.2321 13.3223 12.5469C13.0953 12.8123 12.8478 13.0593 12.584 13.2881C12.5484 13.3246 12.5106 13.3593 12.4668 13.3887C11.3913 14.2811 10.0437 14.8571 8.56738 14.9756C8.56118 14.9762 8.55508 14.978 8.54883 14.9785C8.51409 14.9812 8.4792 14.9822 8.44434 14.9844C8.38882 14.9879 8.3332 14.991 8.27734 14.9932C8.18529 14.9968 8.09287 15 8 15C7.90681 15 7.81406 14.9968 7.72168 14.9932C7.66583 14.991 7.6102 14.9879 7.55469 14.9844C7.52015 14.9822 7.48558 14.9812 7.45117 14.9785C7.44459 14.978 7.43816 14.9763 7.43164 14.9756C5.94988 14.8564 4.59683 14.2772 3.51953 13.3789C3.50616 13.3677 3.49384 13.3556 3.48145 13.3438C3.47213 13.3365 3.46218 13.33 3.45312 13.3223C3.17492 13.0844 2.91561 12.8251 2.67773 12.5469C2.40865 12.2321 2.44515 11.7585 2.75977 11.4893C3.07452 11.2202 3.54818 11.2567 3.81738 11.5713C3.83028 11.5864 3.84339 11.6013 3.85645 11.6162C4.73898 10.612 6.02721 10.0001 7.4248 10H8.5752ZM7.4248 11.5C6.47086 11.5001 5.59107 11.9168 4.9873 12.6016C5.85267 13.1696 6.88689 13.5 8 13.5C9.11327 13.5 10.1472 13.1687 11.0127 12.6006C10.4088 11.9164 9.52878 11.5 8.5752 11.5H7.4248Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M1.82715 6.76172C2.24007 6.79385 2.54868 7.15444 2.5166 7.56738C2.50553 7.70999 2.5 7.85427 2.5 8C2.5 8.14573 2.50553 8.29001 2.5166 8.43262C2.54868 8.84556 2.24007 9.20615 1.82715 9.23828C1.41418 9.27036 1.05357 8.9618 1.02148 8.54883C1.00741 8.36759 1 8.18457 1 8C1 7.81543 1.00741 7.63241 1.02148 7.45117C1.05357 7.0382 1.41418 6.72964 1.82715 6.76172Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M14.1729 6.76172C14.5858 6.72964 14.9464 7.0382 14.9785 7.45117C14.9926 7.63241 15 7.81543 15 8C15 8.18457 14.9926 8.36759 14.9785 8.54883C14.9464 8.9618 14.5858 9.27036 14.1729 9.23828C13.7599 9.20615 13.4513 8.84556 13.4834 8.43262C13.4945 8.29001 13.5 8.14573 13.5 8C13.5 7.85427 13.4945 7.70999 13.4834 7.56738C13.4513 7.15444 13.7599 6.79385 14.1729 6.76172Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M3.45312 2.67773C3.76789 2.40865 4.24155 2.44515 4.51074 2.75977C4.77982 3.07452 4.74329 3.54818 4.42871 3.81738C4.20954 4.00475 4.00475 4.20954 3.81738 4.42871C3.54818 4.74329 3.07452 4.77982 2.75977 4.51074C2.44515 4.24155 2.40865 3.76789 2.67773 3.45312C2.91561 3.17492 3.17492 2.91561 3.45312 2.67773Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M11.4893 2.75977C11.7585 2.44515 12.2321 2.40865 12.5469 2.67773C12.8251 2.91561 13.0844 3.17492 13.3223 3.45312C13.5913 3.76789 13.5548 4.24155 13.2402 4.51074C12.9255 4.77982 12.4518 4.74329 12.1826 4.42871C11.9953 4.20954 11.7905 4.00475 11.5713 3.81738C11.2567 3.54818 11.2202 3.07452 11.4893 2.75977Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M8 1C8.18457 1 8.36759 1.00741 8.54883 1.02148C8.9618 1.05357 9.27036 1.41418 9.23828 1.82715C9.20615 2.24007 8.84556 2.54868 8.43262 2.5166C8.29001 2.50553 8.14573 2.5 8 2.5C7.85427 2.5 7.70999 2.50553 7.56738 2.5166C7.15444 2.54868 6.79385 2.24007 6.76172 1.82715C6.72964 1.41418 7.0382 1.05357 7.45117 1.02148C7.63241 1.00741 7.81543 1 8 1Z"/>
  </svg>
}
function ChevronRightIcon() {
  return <svg aria-hidden="true" className="crs-row-chevron" width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="M5.46967 11.4697C5.17678 11.7626 5.17678 12.2374 5.46967 12.5303C5.76256 12.8232 6.23744 12.8232 6.53033 12.5303L10.5303 8.53033C10.8207 8.23999 10.8236 7.77014 10.5368 7.47624L6.63419 3.47624C6.34492 3.17976 5.87009 3.17391 5.57361 3.46318C5.27713 3.75244 5.27128 4.22728 5.56054 4.52376L8.94583 7.99351L5.46967 11.4697Z"/></svg>
}
function PlusIcon() {
  return <svg aria-hidden="true" width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M8.75 4C8.75 3.58579 8.41421 3.25 8 3.25C7.58579 3.25 7.25 3.58579 7.25 4V7.25H4C3.58579 7.25 3.25 7.58579 3.25 8C3.25 8.41421 3.58579 8.75 4 8.75H7.25V12C7.25 12.4142 7.58579 12.75 8 12.75C8.41421 12.75 8.75 12.4142 8.75 12V8.75H12C12.4142 8.75 12.75 8.41421 12.75 8C12.75 7.58579 12.4142 7.25 12 7.25H8.75V4Z"/></svg>
}
function MoreIcon() {
  return <svg aria-hidden="true" width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M3 6.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Z"/></svg>
}
