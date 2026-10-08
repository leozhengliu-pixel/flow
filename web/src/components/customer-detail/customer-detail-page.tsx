import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'

import { CustomerStatusIcon, CustomerTierIcon, findCustomerStatus, findCustomerTier } from '@/components/customer/customer-status-icon'
import { isMacPlatform } from '@/components/project-detail/project-detail-shortcuts'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { AppLink } from '@/components/ui/app-link'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { NotificationCheckbox, NotificationOptionSection } from '@/components/ui/notification-controls'
import { LinearContextMenuRoot, LinearContextMenuTrigger, LinearDropdownMenuContent, LinearMenuContent, LinearMenuItem, LinearMenuOptions, LinearMenuSeparator, LinearSubmenu } from '@/components/ui/row-context-menu'
import { FlowTooltip } from '@/components/ui/tooltip'
import { UserAvatar } from '@/components/ui/user-avatar'
import { SubscriptionIcon } from '@/components/ui/view-action-icons'
import { useI18n } from '@/i18n/i18n'
import { addSubscription, deleteCustomer, listIssueRecords, removeSubscription, updateCustomer } from '@/lib/api'
import { customerPath, customersPath, issuePath, projectPath } from '@/lib/app-routes'
import { toggleFavoriteFor } from '@/lib/favorites'
import { refreshResourcePreferences } from '@/lib/resource-preferences'
import { isActiveSubscription } from '@/lib/subscription-records'
import type { BootstrapData, Customer, Issue } from '@/types/flow'
import { CustomerDialog } from './customer-dialog'
import { CustomerLogo } from '@/components/customer/customer-logo'
import { CustomerMergeDialog } from './customer-merge-dialog'
import { ArchiveBoxIcon, CustomerSizeIcon, DollarBillIcon, EditPencilIcon, LinkIcon, MergeIcon, MoreDotsIcon, StarOutlineIcon } from './customer-page-icons'
import { CUSTOMER_NOTIFICATION_EVENTS, customerRequestsMarkdown, customerRevenueParts, formatCustomerSize, groupCustomerNeeds, isArchivedNeed, readCustomerPageView, showArchivedStorageKey, writeCustomerPageView, type CustomerPageViewPreferences } from './customer-page-model'
import { CustomerPageRequests } from './customer-page-requests'

import './customer-detail-page.css'
import './customer-detail.css'

const COPY_URL_SHORTCUT = '⌘ ⇧ ,'
const COPY_MARKDOWN_SHORTCUT = '⌘ ⌥ C'

/** Linear's add-request key: Ctrl R on macOS, Ctrl Alt R elsewhere. */
function addRequestShortcut(mac = isMacPlatform()) {
  return mac ? 'Ctrl R' : 'Ctrl Alt R'
}

/**
 * Linear's customer page (CustomerPage.Zn): header with the "Customers ›" breadcrumb, favorite and
 * customer menu, copy-URL and notification buttons; the top section (logo, name, domains, edit,
 * Status / Tier / Revenue / Size / Owner); and the Requests list.
 */
export function CustomerDetailPage({
  data,
  customer,
  onBack,
  onReload,
}: {
  data: BootstrapData
  customer: Customer
  onBack: () => void
  /** Refreshes customer metadata; `issueIds` names issues whose requests changed. */
  onReload: (issueIds?: string[]) => Promise<void>
  /** Kept for callers; rows link to issues and projects directly. */
  onOpenResource?: (type: 'issue' | 'project', id: string) => void
}) {
  const { t } = useI18n()
  const navigate = useNavigate()
  const workspace = data.workspace.urlKey
  const [composerOpen, setComposerOpen] = useState(false)
  const [editOpen, setEditOpen] = useState(false)
  const [mergeTarget, setMergeTarget] = useState<Customer>()
  const [notificationsOpen, setNotificationsOpen] = useState(false)
  const [view, setViewState] = useState<CustomerPageViewPreferences>(readCustomerPageView)
  const setView = (next: CustomerPageViewPreferences) => { setViewState(next); writeCustomerPageView(next) }
  const [showArchived, setShowArchivedState] = useState(() => readFlag(showArchivedStorageKey(customer.id)))
  useEffect(() => { setShowArchivedState(readFlag(showArchivedStorageKey(customer.id))) }, [customer.id])
  const toggleShowArchived = () => setShowArchivedState(current => { writeFlag(showArchivedStorageKey(customer.id), !current); return !current })

  const requests = useMemo(() => data.customerRequests.filter(request => request.customerId === customer.id), [data.customerRequests, customer.id])
  const issueById = useCustomerRequestIssues(data, requests)
  const projectById = useCallback((id: string) => data.projects.find(project => project.id === id), [data.projects])
  const allGroups = useMemo(() => groupCustomerNeeds(requests, issueById, projectById), [requests, issueById, projectById])
  const archivedRequests = requests.filter(request => isArchivedNeed(request, request.issueId ? issueById(request.issueId) : undefined, request.projectId ? projectById(request.projectId) : undefined))
  const groups = useMemo(() => showArchived ? allGroups : groupCustomerNeeds(requests.filter(request => !archivedRequests.includes(request)), issueById, projectById), [allGroups, showArchived, requests, archivedRequests.length, issueById, projectById]) // eslint-disable-line react-hooks/exhaustive-deps

  const favorite = data.favorites.some(item => item.resourceType === 'customer' && item.resourceId === customer.id)
  const subscription = data.subscriptions.find(item => item.resourceType === 'customer' && item.resourceId === customer.id && item.userId === data.viewer.id && isActiveSubscription(item))
  const subscribedEvents = subscription ? (subscription.events?.length ? subscription.events.filter(event => CUSTOMER_NOTIFICATION_EVENTS.some(([name]) => name === event)) : CUSTOMER_NOTIFICATION_EVENTS.map(([name]) => name)) : []
  const manualEditsDisabled = data.workspaceSettings.featureSettings?.customerManualEdits === false
  const url = `${window.location.origin}${customerPath(workspace, customer)}`

  const copy = (value: string, message: string) => void navigator.clipboard.writeText(value).then(() => toast.success(t(message))).catch(() => toast.error(t('Could not copy to clipboard')))
  const copyUrl = () => copy(url, 'Customer URL copied to clipboard')
  const copyMarkdown = () => {
    const markdown = customerRequestsMarkdown(customer.name, allGroups, issue => `${window.location.origin}${issuePath(workspace, issue)}`, project => `${window.location.origin}${projectPath(workspace, project)}`)
    copy(markdown, 'Customer requests copied to clipboard')
  }
  const toggleFavorite = () => void Promise.resolve(toggleFavoriteFor(data, 'customer', customer.id, undefined, favorite)).catch(() => toast.error(t('Could not update favorite')))
  const setEvents = async (events: string[]) => {
    try {
      if (events.length) await addSubscription('customer', customer.id, events)
      else await removeSubscription('customer', customer.id)
      await refreshResourcePreferences(workspace)
    } catch (error) {
      toast.error(t('Could not update customer notifications'), { description: error instanceof Error ? error.message : undefined })
    }
  }
  const toggleEvent = (event: string) => void setEvents(subscribedEvents.includes(event) ? subscribedEvents.filter(item => item !== event) : [...subscribedEvents, event])
  const remove = async () => {
    const description = `${t('Are you sure you want to delete this customer?')}${requests.length ? ` ${t('This will also delete all associated customer requests.')}` : ''}`
    const confirmed = await confirmAction(t('Delete {name}').replace('{name}', customer.name), { description, confirmLabel: t('Delete'), danger: true })
    if (!confirmed) return
    try {
      await deleteCustomer(customer.id)
      onBack()
      await onReload()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not delete customer'))
    }
  }
  const saveCustomer = async (input: Parameters<typeof updateCustomer>[1]) => {
    try {
      await updateCustomer(customer.id, input)
      await onReload()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not update customer'))
      throw error
    }
  }

  const addShortcut = addRequestShortcut()
  useCustomerPageHotkeys({
    onAddRequest: () => setComposerOpen(true),
    onCopyUrl: copyUrl,
    onCopyMarkdown: requests.length ? copyMarkdown : undefined,
    onEscape: () => navigate(customersPath(workspace)),
  })

  const menuItems = <>
    <LinearMenuItem icon={<EditPencilIcon/>} label="Edit…" onSelect={() => setEditOpen(true)}/>
    <LinearMenuSeparator/>
    <LinearMenuItem icon={<LinearGlyph name="favorite"/>} label={favorite ? 'Unfavorite' : 'Favorite'} onSelect={toggleFavorite}/>
    <LinearSubmenu icon={<LinearGlyph name="copy"/>} label="Copy…">
      <LinearMenuItem icon={<LinkIcon/>} label="Copy URL" shortcut={COPY_URL_SHORTCUT} onSelect={copyUrl}/>
      {requests.length > 0 && <LinearMenuItem icon={<LinearGlyph name="copy"/>} label="Copy requests as Markdown" shortcut={COPY_MARKDOWN_SHORTCUT} onSelect={copyMarkdown}/>}
    </LinearSubmenu>
    <LinearSubmenu icon={<LinearGlyph name="subscribe"/>} label="Subscribe">
      <LinearMenuOptions multiple selected={new Set(subscribedEvents)} options={CUSTOMER_NOTIFICATION_EVENTS.map(([id, label]) => ({ id, label }))} onChoose={toggleEvent}/>
    </LinearSubmenu>
    {archivedRequests.length > 0 && <LinearMenuItem icon={<ArchiveBoxIcon/>} label={showArchived ? 'Hide archived requests' : 'Show archived requests'} onSelect={toggleShowArchived}/>}
    <LinearSubmenu icon={<MergeIcon/>} label="Merge with…" search>
      <LinearMenuOptions placeholder="Search for customer to merge with…" selected={new Set()} options={data.customers.filter(item => item.id !== customer.id).sort((a, b) => a.name.localeCompare(b.name)).map(item => ({ id: item.id, label: item.name, translate: false, icon: <CustomerLogo customer={item}/> }))} onChoose={id => setMergeTarget(data.customers.find(item => item.id === id))}/>
    </LinearSubmenu>
    <LinearMenuSeparator/>
    <LinearMenuItem icon={<LinearGlyph name="delete"/>} label="Delete" onSelect={() => void remove()}/>
  </>

  return <main className="main-panel customer-page">
    <header className="customer-page__header">
      <div className="customer-page__crumbs">
        <AppLink className="customer-page__crumb" href={customersPath(workspace)}>{t('Customers')}</AppLink>
        <span aria-hidden="true" className="customer-page__chevron">›</span>
        <LinearContextMenuRoot>
          <LinearContextMenuTrigger asChild>
            <span className="customer-page__title"><CustomerLogo customer={customer}/><span data-i18n-ignore>{customer.name}</span></span>
          </LinearContextMenuTrigger>
          <LinearMenuContent label={t('Customer actions')}>
            <LinearMenuItem icon={<LinkIcon/>} label="Copy URL" shortcut={COPY_URL_SHORTCUT} onSelect={copyUrl}/>
            {requests.length > 0 && <LinearMenuItem icon={<LinearGlyph name="copy"/>} label="Copy requests as Markdown" shortcut={COPY_MARKDOWN_SHORTCUT} onSelect={copyMarkdown}/>}
          </LinearMenuContent>
        </LinearContextMenuRoot>
      </div>
      <FlowTooltip label={t(favorite ? 'Remove from favorites' : 'Add to favorites')}>
        <button type="button" role="switch" aria-checked={favorite} aria-label={t(favorite ? 'Remove from favorites' : 'Add to favorites')} className="customer-page__action" data-active={favorite || undefined} onClick={toggleFavorite}>
          {favorite ? <LinearGlyph name="favorite" className="customer-page__favorite"/> : <StarOutlineIcon/>}
        </button>
      </FlowTooltip>
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild>
          <button type="button" className="customer-page__action" aria-label={t('Open customer menu')}><MoreDotsIcon/></button>
        </DropdownMenu.Trigger>
        <LinearDropdownMenuContent label={t('Customer actions')}>{menuItems}</LinearDropdownMenuContent>
      </DropdownMenu.Root>
      <span className="customer-page__spacer"/>
      <FlowTooltip label={t('Copy customer URL')} shortcut={COPY_URL_SHORTCUT}>
        <button type="button" className="customer-page__action" aria-label={t('Copy page URL')} onClick={copyUrl}><LinkIcon/></button>
      </FlowTooltip>
      <Popover.Root open={notificationsOpen} onOpenChange={setNotificationsOpen}>
        <FlowTooltip label={t('Customer notifications')} disabled={notificationsOpen}>
          <Popover.Trigger asChild>
            <button type="button" className="customer-page__action" aria-label={t('Setup customer notifications')} data-active={subscribedEvents.length > 0 || undefined}><SubscriptionIcon/></button>
          </Popover.Trigger>
        </FlowTooltip>
        <Popover.Portal>
          <Popover.Content data-flow-motion="floating" align="end" className="customer-page__notifications" collisionPadding={16} sideOffset={4}>
            <NotificationOptionSection className="customer-page__notifications-section" title={<><span>{t('Send inbox notifications for')}</span> <span data-i18n-ignore>{customer.name}</span></>}>
              {CUSTOMER_NOTIFICATION_EVENTS.map(([event, label]) => <NotificationCheckbox key={event} checked={subscribedEvents.includes(event)} label={t(label)} onChange={() => toggleEvent(event)}/>)}
            </NotificationOptionSection>
          </Popover.Content>
        </Popover.Portal>
      </Popover.Root>
    </header>
    <div className="customer-page__scroll">
      <div className="customer-page__content">
        <CustomerTopSection data={data} customer={customer} manualEditsDisabled={manualEditsDisabled} onEdit={() => setEditOpen(true)} onSave={saveCustomer}/>
        <hr className="customer-page__separator"/>
        <CustomerPageRequests
          data={data}
          customer={customer}
          groups={groups}
          archivedCount={archivedRequests.length}
          showArchived={showArchived}
          onToggleShowArchived={toggleShowArchived}
          composerOpen={composerOpen}
          onComposerOpenChange={setComposerOpen}
          view={view}
          onViewChange={setView}
          onReload={onReload}
          addShortcut={addShortcut}
        />
      </div>
    </div>
    <CustomerDialog
      currency={data.workspaceSettings.featureSettings?.customerRevenueCurrency}
      customers={data.customers}
      open={editOpen}
      statuses={data.customerStatuses}
      tiers={data.customerTiers}
      users={data.users}
      customer={customer}
      onOpenChange={setEditOpen}
      onSubmit={saveCustomer}
    />
    <CustomerMergeDialog data={data} source={customer} target={mergeTarget} open={Boolean(mergeTarget)} onOpenChange={open => { if (!open) setMergeTarget(undefined) }} onMerged={async (merged, source) => {
      if (source.id === customer.id) navigate(customerPath(workspace, merged))
      await onReload()
    }}/>
  </main>
}

function readFlag(key: string) {
  try { return localStorage.getItem(key) === 'true' } catch { return false }
}
function writeFlag(key: string, value: boolean) {
  try { if (value) localStorage.setItem(key, 'true'); else localStorage.removeItem(key) } catch { /* storage unavailable */ }
}

/**
 * Issues linked by this customer's requests. In paged workspaces the bootstrap holds only some
 * issues, so the missing ones are fetched by id (a scoped lookup, never a full scan).
 */
function useCustomerRequestIssues(data: BootstrapData, requests: BootstrapData['customerRequests']) {
  const [fetched, setFetched] = useState<ReadonlyMap<string, Issue>>(() => new Map())
  const requested = useRef(new Set<string>())
  const loaded = useMemo(() => new Map(data.issues.map(issue => [issue.id, issue])), [data.issues])
  const ids = useMemo(() => [...new Set(requests.map(request => request.issueId).filter((id): id is string => Boolean(id)))], [requests])
  const missingKey = ids.filter(id => !loaded.has(id)).join(',')
  useEffect(() => {
    const missing = missingKey ? missingKey.split(',').filter(id => !requested.current.has(id)) : []
    if (!missing.length) return
    missing.forEach(id => requested.current.add(id))
    let active = true
    void (async () => {
      for (let start = 0; start < missing.length; start += 200) {
        const chunk = missing.slice(start, start + 200)
        try {
          const page = await listIssueRecords({ filter: { field: 'id', operator: 'in', values: chunk }, archived: 'all', limit: chunk.length })
          if (active) setFetched(current => new Map([...current, ...page.items.map(issue => [issue.id, issue] as const)]))
        } catch { chunk.forEach(id => requested.current.delete(id)) }
      }
    })()
    return () => { active = false }
  }, [missingKey])
  return useCallback((id: string) => loaded.get(id) ?? fetched.get(id), [loaded, fetched])
}

function useCustomerPageHotkeys({ onAddRequest, onCopyUrl, onCopyMarkdown, onEscape }: { onAddRequest: () => void; onCopyUrl: () => void; onCopyMarkdown?: () => void; onEscape: () => void }) {
  const handlers = useRef({ onAddRequest, onCopyUrl, onCopyMarkdown, onEscape })
  useEffect(() => { handlers.current = { onAddRequest, onCopyUrl, onCopyMarkdown, onEscape } })
  useEffect(() => {
    const mac = isMacPlatform()
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented) return
      const target = event.target instanceof Element ? event.target : null
      const typing = Boolean(target?.closest('input,textarea,select,[contenteditable="true"],[role="textbox"]'))
      const overlay = Boolean(document.querySelector('[role="menu"],[role="dialog"],[role="alertdialog"]'))
      const key = /^Key[A-Z]$/.test(event.code) ? event.code.slice(3).toLowerCase() : event.key.toLowerCase()
      const mod = mac ? event.metaKey : event.ctrlKey
      // Ctrl R (Ctrl Alt R off macOS) adds a request, also from a text field (Linear: runWithInputFocus).
      if (key === 'r' && event.ctrlKey && !event.metaKey && !event.shiftKey && event.altKey === !mac) {
        if (overlay) return
        event.preventDefault()
        handlers.current.onAddRequest()
        return
      }
      if (mod && event.shiftKey && !event.altKey && (event.key === ',' || event.code === 'Comma')) {
        event.preventDefault()
        handlers.current.onCopyUrl()
        return
      }
      if (mod && event.altKey && !event.shiftKey && key === 'c' && handlers.current.onCopyMarkdown && !window.getSelection()?.toString()) {
        event.preventDefault()
        handlers.current.onCopyMarkdown()
        return
      }
      if (event.key === 'Escape' && !typing && !overlay && !event.metaKey && !event.ctrlKey && !event.altKey && !event.shiftKey) {
        if (document.querySelector('.customer-need-composer')) return
        event.preventDefault()
        handlers.current.onEscape()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])
}

/** Linear's CustomerPageTopSection: identity row and the property row under it. */
function CustomerTopSection({ data, customer, manualEditsDisabled, onEdit, onSave }: { data: BootstrapData; customer: Customer; manualEditsDisabled: boolean; onEdit: () => void; onSave: (input: Parameters<typeof updateCustomer>[1]) => Promise<void> }) {
  const { t, locale } = useI18n()
  const status = findCustomerStatus(data.customerStatuses, customer.status)
  const tier = findCustomerTier(data.customerTiers, customer.tier)
  const tierName = tier?.name ?? customer.tier
  const owner = data.users.find(user => user.id === customer.ownerId)
  const revenue = customerRevenueParts(customer.annualRevenue, data.workspaceSettings.featureSettings, locale)
  const disabledReason = manualEditsDisabled ? t('Manual edits of attributes are disabled') : undefined
  const statuses = data.customerStatuses.filter(item => !item.archivedAt)
  const viewer = data.viewer
  const owners = [viewer, ...data.users.filter(user => user.id !== viewer.id && user.active && !user.app && !user.builtinAgent)]
  const saveQuietly = (input: Parameters<typeof updateCustomer>[1]) => void onSave(input).catch(() => undefined)
  return <section className="customer-top" aria-label={t('Customer')}>
    <div className="customer-top__identity">
      <CustomerLogo customer={customer} size={32}/>
      <div className="customer-top__name">
        <h1 data-i18n-ignore>{customer.name}</h1>
        {customer.domains.length > 0 && <FlowTooltip label={customer.domains.length > 1 ? customer.domains.join(', ') : undefined}>
          <span className="customer-top__domains" data-i18n-ignore>{customer.domains[0]}{customer.domains.length > 1 ? ` +${customer.domains.length - 1}` : ''}</span>
        </FlowTooltip>}
      </div>
      <FlowTooltip label={t('Edit customer…')}>
        <button type="button" className="customer-top__edit" aria-label={t('Edit customer')} onClick={onEdit}><EditPencilIcon size={14}/></button>
      </FlowTooltip>
    </div>
    <div className="customer-top__properties">
      {status && <Property label="Status">
        <DropdownMenu.Root>
          <FlowTooltip label={disabledReason}>
            <DropdownMenu.Trigger asChild disabled={manualEditsDisabled}>
              <button type="button" className="customer-top__button" aria-label={t('Change customer status…')} aria-disabled={manualEditsDisabled || undefined}><CustomerStatusIcon color={status.color}/><span data-i18n-ignore>{status.name}</span></button>
            </DropdownMenu.Trigger>
          </FlowTooltip>
          <LinearDropdownMenuContent label={t('Status')}>
            <LinearMenuOptions selected={new Set([status.id])} options={statuses.map((item, index) => ({ id: item.id, label: item.name, translate: false, shortcut: index < 9 ? String(index + 1) : undefined, icon: <CustomerStatusIcon color={item.color}/> }))} onChoose={id => { const next = statuses.find(item => item.id === id); if (next && next.id !== status.id) saveQuietly({ status: next.name }) }}/>
          </LinearDropdownMenuContent>
        </DropdownMenu.Root>
      </Property>}
      {tierName && <Property label="Tier"><span className="customer-top__static"><CustomerTierIcon size={16}/><span data-i18n-ignore>{tierName}</span></span></Property>}
      {revenue && <Property label="Revenue">
        <FlowTooltip label={revenue.exact}>
          <span className="customer-top__static"><DollarBillIcon/><span className="customer-top__value">{revenue.compact}<span className="customer-top__suffix">{t(revenue.suffix)}</span></span></span>
        </FlowTooltip>
      </Property>}
      {customer.size != null && customer.size > 0 && <Property label="Size"><span className="customer-top__static"><CustomerSizeIcon/><span className="customer-top__value">{formatCustomerSize(customer.size, locale)}</span></span></Property>}
      {owner && <Property label="Owner">
        <DropdownMenu.Root>
          <FlowTooltip label={disabledReason}>
            <DropdownMenu.Trigger asChild disabled={manualEditsDisabled}>
              <button type="button" className="customer-top__button" aria-label={t('Change customer owner…')} aria-disabled={manualEditsDisabled || undefined}><UserAvatar className="customer-top__avatar" avatarUrl={owner.avatarUrl} name={owner.displayName || owner.name}/><span data-i18n-ignore>{owner.displayName || owner.name}</span></button>
            </DropdownMenu.Trigger>
          </FlowTooltip>
          <LinearDropdownMenuContent label={t('Owner')}>
            <LinearMenuOptions placeholder="Change customer owner…" selected={new Set([owner.id])} options={[{ id: '', label: 'No owner', shortcut: '0', icon: <LinearGlyph name="owner"/> }, ...owners.map((user, index) => ({ id: user.id, label: user.displayName || user.name, translate: false, shortcut: index === 0 ? '1' : undefined, icon: <UserAvatar className="linear-menu__avatar" avatarUrl={user.avatarUrl} name={user.displayName || user.name}/> }))]} onChoose={id => { if (id !== owner.id) saveQuietly({ ownerId: id }) }}/>
          </LinearDropdownMenuContent>
        </DropdownMenu.Root>
      </Property>}
    </div>
  </section>
}

function Property({ label, children }: { label: string; children: ReactNode }) {
  const { t } = useI18n()
  return <div className="customer-top__property">
    <span className="customer-top__label">{t(label)}</span>
    {children}
  </div>
}
