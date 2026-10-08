import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Plus, X } from 'lucide-react'
import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'
import { toast } from 'sonner'

import { ContentViewHeaderSearch } from '@/components/content-view/content-view-header-search'
import { CustomerDialog } from '@/components/customer-detail/customer-dialog'
import { CustomerMergeDialog } from '@/components/customer-detail/customer-merge-dialog'
import { CustomerLogo } from '@/components/customer/customer-logo'
import { CustomerStatusIcon, CustomerTierIcon, findCustomerStatus, findCustomerTier } from '@/components/customer/customer-status-icon'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { NoAssigneeIcon } from '@/components/issue/issue-icons'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { LinearContextMenuPortal, LinearContextMenuRoot, LinearContextMenuTrigger, LinearDropdownMenuContent, LinearMenuContent, LinearMenuItem, LinearMenuOptions, LinearMenuSeparator, LinearSubmenu, type LinearMenuOption } from '@/components/ui/row-context-menu'
import { SelectControl } from '@/components/ui/select-control'
import { FlowTooltip } from '@/components/ui/tooltip'
import { UserAvatar } from '@/components/ui/user-avatar'
import { VirtualColumnList } from '@/components/ui/virtual-column-list'
import { useI18n } from '@/i18n/i18n'
import { addSubscription, removeSubscription } from '@/lib/api'
import { customerPath } from '@/lib/app-routes'
import { customerRevenueLabel, formatCustomerRevenue } from '@/lib/customer-settings'
import { toggleFavoriteFor } from '@/lib/favorites'
import { personMatchesQuery } from '@/lib/people'
import { refreshResourcePreferences } from '@/lib/resource-preferences'
import { isActiveSubscription } from '@/lib/subscription-records'
import type { BootstrapData, Customer, CustomerMutationInput, CustomerStatus, User } from '@/types/flow'
import { DirectoryDisplayMenu, DirectoryFilterMenu, type DirectoryFilterGroup } from './directory-menus'
import { CustomersEmptyIllustration } from './customers-empty-illustration'
import {
  activeCustomerFilterFields, CURRENT_USER, customerDirection, customerRequestCounts, customerRowColumns, emptyCustomerFilters, matchesCustomerFilters, matchesCustomerSearch, NO_OWNER, numberOperators, sortCustomers,
  CUSTOMER_DOCS_URL, parseFilterNumber, useCustomerDirectoryPreferences, type CustomerColumn, type CustomerFilterField, type CustomerFilters, type CustomerOrdering, type ListFilter, type NumberOperator,
} from './customers-directory-model'
import './customers-directory.css'

const VIRTUAL_THRESHOLD = 80

type Props = {
  data: BootstrapData
  onCreate: () => void
  onUpdate: (customer: Customer, input: CustomerMutationInput) => Promise<void>
  onDelete: (customer: Customer) => Promise<void>
  onOpen: (customer: Customer) => void
  onResultCount?: (count: number | undefined) => void
  /** Refreshes workspace metadata (after a merge moves requests between customers). */
  onReload?: () => Promise<void> | void
}

/** Linear's Customers page body: toolbar (find, filter, display options), filter chips, column header, rows and empty states. */
export function CustomersDirectory({ data, onCreate, onUpdate, onDelete, onOpen, onResultCount, onReload }: Props) {
  const { t } = useI18n()
  const customers = useMemo(() => data.customers ?? [], [data.customers])
  const statuses = useMemo(() => [...(data.customerStatuses ?? [])].filter(status => !status.archivedAt).sort((a, b) => a.position - b.position), [data.customerStatuses])
  const tiers = useMemo(() => [...(data.customerTiers ?? [])].filter(tier => !tier.archivedAt).sort((a, b) => a.position - b.position), [data.customerTiers])
  const settings = data.workspaceSettings.featureSettings
  const monthly = settings?.customerRevenueFormat === 'monthly'
  const [preferences, setPreferences] = useCustomerDirectoryPreferences(data.workspace.id, data.viewer.id)
  const columns = useMemo(() => new Set(preferences.columns), [preferences.columns])
  const [query, setQuery] = useState('')
  const [filters, setFilters] = useState<CustomerFilters>(emptyCustomerFilters)
  const [editing, setEditing] = useState<Customer>()
  const [merge, setMerge] = useState<{ source: Customer; target: Customer }>()
  const counts = useMemo(() => customerRequestCounts(data.customerRequests ?? []), [data.customerRequests])
  const usersById = useMemo(() => new Map(data.users.map(user => [user.id, user])), [data.users])
  const activeFields = activeCustomerFilterFields(filters)
  const filtering = activeFields.length > 0 || filters.advanced
  const visible = useMemo(() => {
    const context = { viewerId: data.viewer.id, statuses: data.customerStatuses ?? [], tiers: data.customerTiers ?? [] }
    return sortCustomers(customers.filter(customer => matchesCustomerSearch(customer, query) && matchesCustomerFilters(customer, filters, context)), preferences, { counts, statuses: data.customerStatuses ?? [], tiers: data.customerTiers ?? [] })
  }, [counts, customers, data.customerStatuses, data.customerTiers, data.viewer.id, filters, preferences, query])
  useEffect(() => {
    onResultCount?.(query.trim() || filtering ? visible.length : undefined)
    return () => onResultCount?.(undefined)
  }, [filtering, onResultCount, query, visible.length])

  const setList = (field: 'owner' | 'status' | 'tier', value: string, checked: boolean) => setFilters(current => {
    const existing = current[field]
    const values = new Set(existing?.values ?? [])
    if (checked) values.add(value); else values.delete(value)
    return { ...current, [field]: values.size ? { operator: existing?.operator ?? 'is', values: [...values] } : undefined }
  })
  const setNumber = (field: 'revenue' | 'size', operator: NumberOperator, raw: number) => setFilters(current => ({ ...current, [field]: { operator, value: field === 'revenue' && monthly ? raw * 12 : raw } }))
  const revenueText = (annual: number) => formatCurrency(monthly ? Math.round(annual / 12) : annual, settings?.customerRevenueCurrency)
  const numberOptions = (field: 'revenue' | 'size') => (value: string) => {
    const parsed = parseFilterNumber(value)
    if (parsed === undefined) return []
    const text = field === 'revenue' ? formatCurrency(parsed, settings?.customerRevenueCurrency) : parsed.toLocaleString('en-US')
    return numberOperators.map(operator => ({ id: operator.id, label: <><span className="customers-filter-operator">{t(operator.label)} </span><span data-i18n-ignore>{text}</span></> }))
  }
  const viewer = data.viewer
  const ownerChoices = [
    { id: NO_OWNER, label: t('No owner'), icon: <NoAssigneeIcon size={16}/> },
    { id: CURRENT_USER, label: t('Current user'), icon: <UserAvatar avatarUrl={viewer.avatarUrl} className="linear-menu__avatar" name={viewer.displayName}/> },
    ...data.users.filter(user => !user.app).map(user => ({ id: user.id, label: user.displayName || user.name, entity: true, keywords: `${user.name} ${user.email ?? ''}`, person: user, icon: <UserAvatar avatarUrl={user.avatarUrl} className="linear-menu__avatar" name={user.displayName}/> })),
  ]
  const filterGroups: DirectoryFilterGroup[] = [
    { id: 'owner', label: t('Owner'), icon: <LinearGlyph name="owner" size={16}/>, choices: ownerChoices, hideSearch: true },
    ...(statuses.length ? [{ id: 'status', label: t('Status'), icon: <StatusSquare/>, choices: statuses.map(status => ({ id: status.id, label: status.name, entity: true, icon: <StatusSquare color={status.color}/> })) }] : []),
    ...(tiers.length ? [{ id: 'tier', label: t('Tier'), icon: <CustomerTierIcon/>, choices: tiers.map(tier => ({ id: tier.id, label: tier.name, entity: true })) }] : []),
    { id: 'revenue', label: t('Revenue'), icon: <SpriteGlyph id="DollarBill"/>, input: { placeholder: t('Enter revenue…'), onSubmit: () => undefined, options: numberOptions('revenue'), onChoose: (id, value) => { const parsed = parseFilterNumber(value); if (parsed !== undefined) setNumber('revenue', id as NumberOperator, parsed) } } },
    { id: 'size', label: t('Size'), icon: <SpriteGlyph id="Users"/>, input: { placeholder: t('Enter size…'), onSubmit: () => undefined, options: numberOptions('size'), onChoose: (id, value) => { const parsed = parseFilterNumber(value); if (parsed !== undefined) setNumber('size', id as NumberOperator, parsed) } } },
  ]
  const selected = { owner: new Set(filters.owner?.values), status: new Set(filters.status?.values), tier: new Set(filters.tier?.values) }
  const onChoice = (groupId: string, choiceId: string, checked: boolean) => { if (groupId === 'owner' || groupId === 'status' || groupId === 'tier') setList(groupId, choiceId, checked) }
  const changeOrdering = (ordering: CustomerOrdering) => setPreferences(current => ({ ...current, ordering, direction: undefined }))
  const sortBy = (ordering: CustomerOrdering) => setPreferences(current => current.ordering === ordering
    ? { ...current, direction: customerDirection(current) === 'asc' ? 'desc' : 'asc' }
    : { ...current, ordering, direction: undefined })
  const toggleColumn = (column: CustomerColumn) => setPreferences(current => ({ ...current, columns: current.columns.includes(column) ? current.columns.filter(item => item !== column) : [...current.columns, column] }))
  const revenueHeader = t(customerRevenueLabel(settings))
  const shown = customerRowColumns.filter(column => columns.has(column) && (column !== 'source' || hasDataSource(data)))
  const grid = useMemo(() => customerGrid(shown), [shown])

  const rows = useRef<HTMLDivElement>(null)
  const onRowsKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!['ArrowDown', 'ArrowUp', 'j', 'k'].includes(event.key) || event.metaKey || event.ctrlKey || event.altKey) return
    const items = [...rows.current?.querySelectorAll<HTMLElement>('.customers-row') ?? []]
    if (!items.length) return
    event.preventDefault()
    const index = items.indexOf(document.activeElement as HTMLElement)
    const next = event.key === 'ArrowDown' || event.key === 'j' ? Math.min(items.length - 1, index + 1) : Math.max(0, index - 1)
    items[index < 0 ? 0 : next]?.focus()
  }

  const header = <div className="customers-columns" role="row" style={{ gridTemplateColumns: grid }}>
    <SortHeader active={preferences.ordering === 'name'} direction={customerDirection(preferences)} label={t('Name')} onClick={() => sortBy('name')}/>
    {shown.map(column => column === 'owner' || column === 'domains' || column === 'source'
      ? <span className="customers-column-label" key={column}>{t(column === 'owner' ? 'Owner' : column === 'domains' ? 'Domains' : 'Data source')}</span>
      : <SortHeader key={column} active={preferences.ordering === column} direction={customerDirection(preferences)} end={column === 'requests' || column === 'revenue' || column === 'size'} label={column === 'requests' ? t('Requests') : column === 'revenue' ? revenueHeader : t(column === 'size' ? 'Size' : column === 'status' ? 'Status' : 'Tier')} onClick={() => sortBy(column as CustomerOrdering)}/>)}
  </div>
  const renderRow = (customer: Customer) => <CustomerRowMenu key={customer.id} customer={customer} data={data} onDelete={onDelete} onEdit={() => setEditing(customer)} onMerge={target => setMerge({ source: customer, target })}>
    <a
      className="customers-row"
      style={{ gridTemplateColumns: grid }}
      href={customerPath(data.workspace.urlKey, customer)}
      onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return; event.preventDefault(); onOpen(customer) }}
    >
      <span className="customers-cell customers-cell--name">
        <CustomerLogo customer={customer} size={16}/>
        <FlowTooltip label={customer.name}><span className="customers-name" data-i18n-ignore>{customer.name}</span></FlowTooltip>
      </span>
      {shown.map(column => <CustomerCell key={column} column={column} customer={customer} count={counts.get(customer.id) ?? 0} data={data} statuses={statuses} owner={usersById.get(customer.ownerId ?? '')} onUpdate={onUpdate}/>)}
    </a>
  </CustomerRowMenu>

  return <>
    <div className="workspace-directory__toolbar workspace-members-toolbar customers-toolbar">
      <ContentViewHeaderSearch alwaysVisible aria-label={t('Find by name or domain…')} className="workspace-members-cvh-search" placeholder={t('Find by name or domain…')} value={query} onChange={setQuery}/>
      <span/>
      <DirectoryFilterMenu
        groups={filterGroups}
        menuClassName="customers-filter-menu"
        submenuClassName="customers-filter-menu"
        onAdvanced={() => setFilters(current => ({ ...current, advanced: true }))}
        onChoice={onChoice}
        selected={selected}
        triggerNode={filtering ? <button aria-label={t('Add another filter')} className="workspace-directory__icon-button" type="button"><FilterGlyph/></button> : undefined}
        tooltip={{ label: t('Filter'), shortcut: 'F' }}
      />
      <DirectoryDisplayMenu<CustomerColumn, CustomerOrdering>
        className="is-compact"
        descending={customerDirection(preferences) === 'desc'}
        onDirection={() => setPreferences(current => ({ ...current, direction: customerDirection(current) === 'asc' ? 'desc' : 'asc' }))}
        onOrdering={changeOrdering}
        onProperty={toggleColumn}
        ordering={preferences.ordering}
        orderingOptions={[
          { id: 'requests', label: t('Request count') },
          { id: 'created', label: t('Created') },
          { id: 'name', label: t('Name') },
          { id: 'revenue', label: revenueHeader },
          { id: 'size', label: t('Size') },
          { id: 'status', label: t('Status') },
          { id: 'tier', label: t('Tier') },
        ]}
        properties={columns}
        propertyOptions={[
          { id: 'requests', label: t('Requests') },
          { id: 'revenue', label: revenueHeader },
          { id: 'size', label: t('Size') },
          { id: 'owner', label: t('Owner') },
          { id: 'status', label: t('Status') },
          { id: 'tier', label: t('Tier') },
          { id: 'domains', label: t('Domains') },
          { id: 'source', label: t('Data source') },
        ]}
      />
    </div>
    {filtering && <CustomerFilterBar filters={filters} groups={filterGroups} selected={selected} onChange={setFilters} onChoice={onChoice} revenueText={revenueText} statuses={statuses} tiers={tiers} users={data.users}/>}
    {customers.length === 0 ? <CustomersEmptyState onCreate={onCreate}/>
      : visible.length === 0 ? <div className="customers-no-results" role="status">
        {filtering ? <>
          <strong>{t('No customers matching the filters')}</strong>
          <button className="customers-pill" type="button" onClick={() => setFilters(emptyCustomerFilters())}>{t('Clear filters')}</button>
        </> : <strong>{t('No results')}</strong>}
      </div>
      : <div className={`customers-table${visible.length > VIRTUAL_THRESHOLD ? ' is-virtualized' : ''}`} ref={rows} onKeyDown={onRowsKeyDown}>
        {visible.length > VIRTUAL_THRESHOLD
          ? <VirtualColumnList header={header} scrollerClassName="customers-virtual-list" data={visible} computeItemKey={(_index, customer) => customer.id} increaseViewportBy={{ top: 220, bottom: 440 }} itemContent={(_index, customer) => renderRow(customer)}/>
          : <>{header}<div className="customers-rows">{visible.map(renderRow)}</div></>}
      </div>}
    {merge && <CustomerMergeDialog data={data} source={merge.source} target={merge.target} open onOpenChange={open => { if (!open) setMerge(undefined) }} onMerged={async () => { await onReload?.() }}/>}
    <CustomerDialog
      currency={settings?.customerRevenueCurrency}
      customer={editing}
      customers={customers}
      open={Boolean(editing)}
      monthlyRevenue={monthly}
      revenueLabel={revenueHeader}
      statuses={data.customerStatuses}
      tiers={data.customerTiers}
      users={data.users}
      onOpenChange={open => { if (!open) setEditing(undefined) }}
      onSubmit={async input => { if (editing) await onUpdate(editing, input) }}
    />
  </>
}

/** Linear's ViewEmptyState for customers: illustration, title, paragraph, primary + Documentation pills. */
export function CustomersEmptyState({ onCreate }: { onCreate: () => void }) {
  const { t } = useI18n()
  return <div className="customers-empty">
    <div className="customers-empty__column">
      <div className="customers-empty__icon"><CustomersEmptyIllustration/></div>
      <div className="customers-empty__copy">
        <span className="customers-empty__title">{t('Customers')}</span>
        <span className="customers-empty__text">{t('Add organizations using your product to track their feature requests and use attributes like revenue and size to prioritize development.')}</span>
      </div>
      <div className="customers-empty__actions">
        <button aria-label={t('Create new customer')} className="customers-pill is-primary" type="button" onClick={onCreate}>{t('Create new customer')}</button>
        <a className="customers-pill" href={CUSTOMER_DOCS_URL} rel="noopener noreferrer" target="_blank">{t('Documentation')}</a>
      </div>
    </div>
  </div>
}

function SortHeader({ label, active, direction, end = false, onClick }: { label: string; active: boolean; direction: 'asc' | 'desc'; end?: boolean; onClick: () => void }) {
  const { t } = useI18n()
  return <button
    aria-label={active ? `${t('Order by')} ${label}, ${t(direction === 'asc' ? 'sorted ascending' : 'sorted descending')}` : `${t('Order by')} ${label}`}
    className={`customers-sort${active ? ' is-active' : ''}${end ? ' is-end' : ''}`}
    type="button"
    onClick={onClick}
  >
    <span>{label}</span>
    <svg aria-hidden="true" fill="currentColor" height="12" viewBox="0 0 16 16" width="12" style={{ transform: active && direction === 'asc' ? 'rotate(180deg)' : undefined }}>
      <path d="M8.75 3a.75.75 0 0 0-1.5 0v8.19L4.53 8.47a.75.75 0 0 0-1.06 1.06l4 4a.75.75 0 0 0 1.06 0l4-4a.75.75 0 0 0-1.06-1.06l-2.72 2.72V3Z"/>
    </svg>
  </button>
}

function CustomerCell({ column, customer, count, data, statuses, owner, onUpdate }: { column: CustomerColumn; customer: Customer; count: number; data: BootstrapData; statuses: CustomerStatus[]; owner?: User; onUpdate: (customer: Customer, input: CustomerMutationInput) => Promise<void> }) {
  const settings = data.workspaceSettings.featureSettings
  if (column === 'requests') return <span className="customers-cell is-end">{count ? <span className="customers-count" data-i18n-ignore>{count.toLocaleString('en-US')}</span> : null}</span>
  if (column === 'revenue') {
    if (!customer.annualRevenue) return <span className="customers-cell is-end"/>
    const exact = formatCurrency(settings?.customerRevenueFormat === 'monthly' ? Math.round(customer.annualRevenue / 12) : customer.annualRevenue, settings?.customerRevenueCurrency)
    return <span className="customers-cell is-end"><FlowTooltip label={exact}><span className="customers-value is-numeric" data-i18n-ignore>{formatCustomerRevenue(customer.annualRevenue, settings, 'en-US')}</span></FlowTooltip></span>
  }
  if (column === 'size') return <span className="customers-cell is-end">{customer.size ? <span className="customers-value is-numeric" data-i18n-ignore>{customer.size.toLocaleString('en-US')}</span> : null}</span>
  if (column === 'status') return <span className="customers-cell customers-cell--picker"><CustomerStatusPicker customer={customer} statuses={statuses} onUpdate={onUpdate}/></span>
  if (column === 'tier') {
    const tier = customer.tier ? findCustomerTier(data.customerTiers, customer.tier)?.name ?? customer.tier : ''
    return <span className="customers-cell">{tier ? <span className="customers-value" data-i18n-ignore>{tier}</span> : null}</span>
  }
  if (column === 'owner') return <span className="customers-cell customers-cell--picker"><CustomerOwnerPicker customer={customer} owner={owner} users={data.users} onUpdate={onUpdate}/></span>
  if (column === 'domains') {
    const domains = customer.domains.join(', ')
    return <span className="customers-cell">{domains ? <FlowTooltip label={domains}><span className="customers-value" data-i18n-ignore>{domains}</span></FlowTooltip> : null}</span>
  }
  return <span className="customers-cell"/>
}

/** Stops a picker inside the row link from opening the customer. */
const stop = (event: MouseEvent | KeyboardEvent) => { event.stopPropagation(); if ('button' in event) event.preventDefault() }

function CustomerStatusPicker({ customer, statuses, onUpdate }: { customer: Customer; statuses: CustomerStatus[]; onUpdate: (customer: Customer, input: CustomerMutationInput) => Promise<void> }) {
  const { t } = useI18n()
  const status = findCustomerStatus(statuses, customer.status)
  const options: LinearMenuOption[] = statuses.map((item, index) => ({ id: item.id, label: item.name, translate: false, icon: <CustomerStatusIcon color={item.color}/>, shortcut: index < 9 ? String(index + 1) : undefined }))
  return <DropdownMenu.Root>
    <DropdownMenu.Trigger asChild>
      <button aria-label={t('Change customer status')} className="customers-picker" type="button" onClick={stop} onPointerDown={event => event.stopPropagation()} onKeyDown={event => event.stopPropagation()}>
        {status ? <><CustomerStatusIcon color={status.color}/><span data-i18n-ignore>{status.name}</span></> : <span data-i18n-ignore>{customer.status}</span>}
      </button>
    </DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <LinearDropdownMenuContent className="customers-picker-menu" label={t('Change customer status…')}>
        <LinearMenuOptions options={options} placeholder="Change customer status…" selected={new Set(status ? [status.id] : [])} onChoose={id => void onUpdate(customer, { status: id })}/>
      </LinearDropdownMenuContent>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
}

function CustomerOwnerPicker({ customer, owner, users, onUpdate }: { customer: Customer; owner?: User; users: User[]; onUpdate: (customer: Customer, input: CustomerMutationInput) => Promise<void> }) {
  const { t } = useI18n()
  const people = users.filter(user => !user.app)
  const options: LinearMenuOption[] = [
    { id: '', label: 'No owner', icon: <NoAssigneeIcon size={16}/>, shortcut: '0' },
    ...people.map((user, index) => ({ id: user.id, label: user.displayName || user.name, translate: false, keywords: `${user.name} ${user.email ?? ''}`, icon: <UserAvatar avatarUrl={user.avatarUrl} className="linear-menu__avatar" name={user.displayName}/>, shortcut: index < 9 ? String(index + 1) : undefined })),
  ]
  return <DropdownMenu.Root>
    <DropdownMenu.Trigger asChild>
      <button aria-label={owner ? `${t('Owner')}: ${owner.displayName}` : t('Change customer owner')} className="customers-picker is-icon" title={owner?.displayName} type="button" onClick={stop} onPointerDown={event => event.stopPropagation()} onKeyDown={event => event.stopPropagation()}>
        {owner ? <UserAvatar avatarUrl={owner.avatarUrl} className="avatar customers-avatar" name={owner.displayName}/> : <NoAssigneeIcon size={16}/>}
      </button>
    </DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <LinearDropdownMenuContent className="customers-picker-menu" label={t('Change customer owner…')}>
        <LinearMenuOptions
          matches={(option, needle) => option.id ? personMatchesQuery(people.find(user => user.id === option.id)!, needle) : t('No owner').toLocaleLowerCase().includes(needle)}
          options={options}
          placeholder="Change customer owner…"
          selected={new Set([customer.ownerId ?? ''])}
          onChoose={id => void onUpdate(customer, { ownerId: id })}
        />
      </LinearDropdownMenuContent>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
}

/** Right-click menu on a customer row (Linear's customer actions: Edit…, Favorite, Copy…, Subscribe, Delete). */
function CustomerRowMenu({ children, customer, data, onEdit, onDelete, onMerge }: { children: ReactNode; customer: Customer; data: BootstrapData; onEdit: () => void; onDelete: (customer: Customer) => Promise<void>; onMerge: (target: Customer) => void }) {
  const { t } = useI18n()
  const favorite = data.favorites.some(item => item.userId === data.viewer.id && item.resourceType === 'customer' && item.resourceId === customer.id)
  const subscribed = data.subscriptions.some(item => item.userId === data.viewer.id && item.resourceType === 'customer' && item.resourceId === customer.id && isActiveSubscription(item))
  const url = `${window.location.origin}${customerPath(data.workspace.urlKey, customer)}`
  const copyUrl = () => void navigator.clipboard.writeText(url).then(() => toast.success(t('Customer URL copied to clipboard')))
  const toggleSubscription = () => void (subscribed ? removeSubscription('customer', customer.id) : addSubscription('customer', customer.id))
    .then(() => refreshResourcePreferences(data.workspace.urlKey))
    .then(() => toast.success(t(subscribed ? 'Unsubscribed from customer' : 'Subscribed to customer')))
  return <LinearContextMenuRoot>
    <LinearContextMenuTrigger asChild>{children}</LinearContextMenuTrigger>
    <LinearContextMenuPortal>
      <LinearMenuContent className="customers-row-menu" label={t('Customer actions')}>
        <LinearMenuItem icon={<LinearGlyph name="edit"/>} label="Edit…" onSelect={onEdit}/>
        <LinearMenuSeparator/>
        <LinearMenuItem icon={<LinearGlyph name="favorite"/>} label={favorite ? 'Unfavorite' : 'Favorite'} onSelect={() => void toggleFavoriteFor(data, 'customer', customer.id, undefined, favorite)}/>
        <LinearSubmenu icon={<LinearGlyph name="copy"/>} label="Copy…">
          <LinearMenuItem icon={<IssueActionGlyph label="Copy URL" fallback={null}/>} label="Copy URL" shortcut="⌘ ⇧ ," onSelect={copyUrl}/>
        </LinearSubmenu>
        <LinearMenuItem icon={<LinearGlyph name="subscribe"/>} label={subscribed ? 'Unsubscribe' : 'Subscribe'} onSelect={toggleSubscription}/>
        <LinearMenuSeparator/>
        <LinearSubmenu icon={<MergeGlyph/>} label="Merge with…" search>
          <LinearMenuOptions
            options={(data.customers ?? []).filter(item => item.id !== customer.id).map(item => ({ id: item.id, label: item.name, translate: false, keywords: item.domains.join(' '), icon: <CustomerLogo customer={item} size={16}/> }))}
            placeholder="Search for customer to merge with…"
            selected={new Set()}
            onChoose={id => { const target = data.customers?.find(item => item.id === id); if (target) onMerge(target) }}
          />
        </LinearSubmenu>
        <LinearMenuSeparator/>
        <LinearMenuItem icon={<LinearGlyph name="delete"/>} label="Delete" onSelect={() => void onDelete(customer)}/>
      </LinearMenuContent>
    </LinearContextMenuPortal>
  </LinearContextMenuRoot>
}

function CustomerFilterBar({ filters, groups, selected, onChange, onChoice, revenueText, statuses, tiers, users }: {
  filters: CustomerFilters
  groups: DirectoryFilterGroup[]
  selected: Record<string, Set<string>>
  onChange: (update: (filters: CustomerFilters) => CustomerFilters) => void
  onChoice: (groupId: string, choiceId: string, checked: boolean) => void
  revenueText: (annual: number) => string
  statuses: CustomerStatus[]
  tiers: { id: string; name: string }[]
  users: User[]
}) {
  const { t } = useI18n()
  const fields = activeCustomerFilterFields(filters)
  const remove = (field: CustomerFilterField) => onChange(current => ({ ...current, [field]: undefined }))
  const listLabel = (field: 'owner' | 'status' | 'tier', filter: ListFilter) => {
    if (filter.values.length > 1) return `${filter.values.length} ${t(field === 'owner' ? 'users' : field === 'status' ? 'statuses' : 'tiers')}`
    const [value] = filter.values
    if (field === 'owner') return value === NO_OWNER ? t('No owner') : value === CURRENT_USER ? t('Current user') : users.find(user => user.id === value)?.displayName ?? value
    if (field === 'status') return statuses.find(status => status.id === value)?.name ?? value
    return tiers.find(tier => tier.id === value)?.name ?? value
  }
  return <div className="customers-filter-bar" aria-label={t('Customer filters')} role="group">
    {filters.advanced && <div className="customers-filter-bar__match">
      <span>{t('Match')}</span>
      <SelectControl label={t('Filter conjunction')} value={filters.conjunction} options={[{ value: 'and', label: t('all filters') }, { value: 'or', label: t('any filter') }]} onChange={value => onChange(current => ({ ...current, conjunction: value as 'and' | 'or' }))}/>
      <button aria-label={t('Remove advanced filter')} className="customers-filter-bar__icon" type="button" onClick={() => onChange(current => ({ ...current, advanced: false, conjunction: 'and' }))}><X size={12}/></button>
    </div>}
    <div className="customers-filter-bar__chips">
      {fields.map(field => {
        const group = groups.find(item => item.id === field)
        if (!group) return null
        const number = field === 'revenue' || field === 'size'
        const filter = filters[field]!
        const many = !number && (filter as ListFilter).values.length > 1
        const operators = number ? numberOperators.map(operator => ({ id: operator.id, label: operator.label })) : [{ id: 'is', label: many ? 'is any of' : 'is' }, { id: 'isNot', label: many ? 'is none of' : 'is not' }]
        const operatorLabel = operators.find(operator => operator.id === filter.operator)?.label ?? ''
        const value = number ? (field === 'revenue' ? revenueText((filter as { value: number }).value) : (filter as { value: number }).value.toLocaleString('en-US')) : listLabel(field as 'owner' | 'status' | 'tier', filter as ListFilter)
        return <div className="customers-filter-chip" key={field}>
          <span className="customers-filter-chip__segment is-label">{group.icon}<span>{group.label}</span></span>
          <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild>
              <button aria-label={`${group.label} ${t('operator')}`} className="customers-filter-chip__segment is-operator" type="button">{t(operatorLabel)}</button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <LinearDropdownMenuContent className="customers-picker-menu" label={t(operatorLabel)}>
                <LinearMenuOptions options={operators} selected={new Set([filter.operator])} onChoose={operator => onChange(current => ({ ...current, [field]: { ...current[field]!, operator } }))}/>
              </LinearDropdownMenuContent>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
          <DirectoryFilterMenu groups={[group]} selected={selected} onChoice={onChoice} showAdvanced={false} trigger="add" triggerNode={<button aria-label={`${group.label} ${t('values')}`} className="customers-filter-chip__segment is-value" data-i18n-ignore type="button">{value}</button>}/>
          <button aria-label={`${t('Remove filter')} ${group.label}`} className="customers-filter-chip__segment is-remove" type="button" onClick={() => remove(field)}><X size={12}/></button>
        </div>
      })}
      <DirectoryFilterMenu groups={groups} selected={selected} onChoice={onChoice} onAdvanced={() => onChange(current => ({ ...current, advanced: true }))} trigger="add" triggerNode={<button aria-label={t('Add another filter')} className="customers-filter-bar__icon is-add" type="button"><Plus size={14}/></button>}/>
    </div>
    <button aria-label={t('Clear all filters')} className="customers-filter-bar__clear" type="button" onClick={() => onChange(() => emptyCustomerFilters())}>{t('Clear')}</button>
  </div>
}

/** Linear sizes columns from content; fixed tracks keep every row aligned (min/max from its column config). */
function customerGrid(columns: readonly CustomerColumn[]) {
  const widths: Record<CustomerColumn, string> = { requests: '64px', revenue: '80px', size: '56px', status: '120px', tier: '100px', owner: '40px', domains: '160px', source: '100px' }
  return ['minmax(120px, 1fr)', ...columns.map(column => widths[column])].join(' ')
}

/** Data source column only applies once a customer data-source integration is connected (Linear). */
function hasDataSource(data: BootstrapData) {
  return Boolean((data as { customerDataSource?: unknown }).customerDataSource)
}

function formatCurrency(value: number, currency = 'USD') {
  try { return new Intl.NumberFormat('en-US', { style: 'currency', currency: /^[A-Z]{3}$/.test(currency) ? currency : 'USD', maximumFractionDigits: 0 }).format(value) }
  catch { return `$${value.toLocaleString('en-US')}` }
}

function SpriteGlyph({ id }: { id: string }) {
  return <svg aria-hidden="true" fill="currentColor" height="16" viewBox="0 0 16 16" width="16"><use href={`/flow-view-icons.svg#${id}`}/></svg>
}

/** Linear's merge glyph (two overlapping cards). */
function MergeGlyph() {
  return <svg aria-hidden="true" fill="currentColor" height="16" viewBox="0 0 16 16" width="16"><path d="M12.75 1A2.25 2.25 0 0 1 15 3.25v5.49a2.25 2.25 0 0 1-2.25 2.25h-1.777v1.75a2.25 2.25 0 0 1-2.25 2.25H3.25A2.25 2.25 0 0 1 1 12.74V7.25A2.25 2.25 0 0 1 3.25 5l1.776-.001V3.25A2.25 2.25 0 0 1 7.277 1h5.474ZM5.026 6.499 3.25 6.5a.75.75 0 0 0-.75.75v5.49c0 .414.336.75.75.75h5.473a.75.75 0 0 0 .75-.75v-1.75H7.277a2.25 2.25 0 0 1-2.25-2.25L5.026 6.5ZM12.75 2.5H7.277a.75.75 0 0 0-.75.75l-.001 1.749L8.723 5a2.25 2.25 0 0 1 2.25 2.25v2.24h1.777a.75.75 0 0 0 .75-.75V3.25a.75.75 0 0 0-.75-.75Z"/></svg>
}

/** Linear's customer status square (8×8, 1px radius) in a 16px icon box; monochrome without a colour. */
function StatusSquare({ color }: { color?: string }) {
  return <svg aria-hidden="true" className="customers-status-square" height="16" viewBox="0 0 16 16" width="16"><rect fill={color ?? 'currentColor'} height="8" rx="1" width="8" x="4" y="4"/></svg>
}


function FilterGlyph() {
  return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path clipRule="evenodd" d="M14.25 3a.75.75 0 0 1 0 1.5H1.75a.75.75 0 0 1 0-1.5h12.5ZM4 8a.75.75 0 0 1 .75-.75h6.5a.75.75 0 0 1 0 1.5h-6.5A.75.75 0 0 1 4 8Zm2.75 3.5a.75.75 0 0 0 0 1.5h2.5a.75.75 0 0 0 0-1.5h-2.5Z" fillRule="evenodd"/></svg>
}

