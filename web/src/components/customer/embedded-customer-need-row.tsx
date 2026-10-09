import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { useState, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'
import { toast } from 'sonner'
import { MentionBody } from '@/components/editor/mentions/mention-body'
import { StatusIcon } from '@/components/issue/issue-icons'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { useIssueSearch } from '@/components/issue/use-issue-search'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { AppLink } from '@/components/ui/app-link'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { LinearDropdownMenuContent, LinearMenuItem, LinearMenuOptions, LinearMenuSearch, LinearMenuSeparator, LinearSubmenu, type LinearMenuOption } from '@/components/ui/row-context-menu'
import { FlowTooltip } from '@/components/ui/tooltip'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { createIssue, deleteCustomerRequest, updateCustomerRequest } from '@/lib/api'
import { customerPath, issuePath, projectPath } from '@/lib/app-routes'
import type { BootstrapData, Customer, CustomerRequest, Issue } from '@/types/flow'
import { CustomerLogo } from './customer-logo'
import { ImportantIcon, MoreIcon, NotImportantIcon, RequestPlusIcon, SourceLinkIcon } from './customer-request-glyphs'
import { compactRelativeTime, groupIsImportant, isImportantRequest, requestSubtitle, requestTimestamp, sourceHost, type CustomerRequestGroup } from './customer-request-model'
import { EmbeddedCustomerNeedForm, type EmbeddedCustomerNeedHost } from './embedded-customer-need-form'
import './embedded-customer-need-row.css'

/** `#customerRequest-{id}` links open and reveal a request (shared with the customer page). */
export const CUSTOMER_REQUEST_HASH = 'customerRequest-'

export type CustomerRequestRowCallbacks = {
  /** A request was created, updated, moved or re-assigned from this row. */
  onChanged?: (request: CustomerRequest) => void
  onDeleted?: (requestId: string) => void
}

export type EmbeddedCustomerNeedRowProps = CustomerRequestRowCallbacks & {
  data: BootstrapData
  group: CustomerRequestGroup
  variant: EmbeddedCustomerNeedHost
  /** Project page: requests on the project's issues show a link to their issue. */
  issueFor?: (request: CustomerRequest) => Issue | undefined
  defaultExpanded?: boolean
}

/**
 * Linear's EmbeddedCustomerNeedRow (the "removed background" design): a 36px header with the
 * customer, a one-line preview, the request count, the important mark and the date — swapped for
 * the source link and "…" on hover — expanding to the full request(s).
 */
export function EmbeddedCustomerNeedRow({ data, group, variant, issueFor, defaultExpanded = false, onChanged, onDeleted }: EmbeddedCustomerNeedRowProps) {
  const { t } = useI18n()
  const { primary, additional, customer } = group
  const hasAdditional = additional.length > 0
  const singleEmpty = !primary.body.trim() && !hasAdditional && !primary.attachments?.length
  const [expanded, setExpanded] = useState(defaultExpanded)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const open = expanded && (!singleEmpty || adding || editingId === primary.id)
  const important = groupIsImportant(group)
  const preview = requestSubtitle(primary)

  const toggle = () => {
    if (singleEmpty && !adding && !primary.archivedAt) {
      setEditingId(primary.id)
      setExpanded(true)
      return
    }
    setExpanded(value => !value)
  }
  const onRowClick = (event: MouseEvent<HTMLDivElement>) => {
    if ((event.target as Element).closest('button, a, input, textarea, [role=menuitem], .embedded-customer-need-form')) return
    if (open && (event.target as Element).closest('.customer-request-row__details')) return
    toggle()
  }
  const onRowKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.target !== event.currentTarget || event.key !== 'Enter') return
    event.preventDefault()
    toggle()
  }

  return <div
    className={`customer-request-row${open ? ' is-expanded' : ''}${menuOpen ? ' is-menu-open' : ''}${primary.archivedAt ? ' is-archived' : ''}`}
    data-customer-request={primary.id}
    id={`${CUSTOMER_REQUEST_HASH}${primary.id}`}
    role="group"
    aria-label={customer?.name ?? t('Unknown customer')}
    tabIndex={0}
    onClick={onRowClick}
    onKeyDown={onRowKeyDown}
  >
    <div className="customer-request-row__header">
      <div className="customer-request-row__lead">
        <CustomerChip data={data} customer={customer} request={primary} onChanged={onChanged}/>
        {open || !preview
          ? <>
              <span className="customer-request-row__spacer"/>
              {open && !hasAdditional && <AddedBy request={primary}/>}
            </>
          : <span className="customer-request-row__preview">{preview}</span>}
        {hasAdditional && <FlowTooltip label={t('Number of requests')}><span className="customer-request-row__count">{additional.length + 1}</span></FlowTooltip>}
        {important && <FlowTooltip label={t('Important')}><span className="customer-request-row__important" aria-label={t('Important')}><ImportantIcon size={16}/></span></FlowTooltip>}
      </div>
      <div className={`customer-request-row__trailing${open ? ' is-expanded' : ''}`}>
        <FlowTooltip label={requestTimestamp(primary.createdAt)}>
          <span className="customer-request-row__date">{compactRelativeTime(primary.createdAt)}</span>
        </FlowTooltip>
        <div className="customer-request-row__actions">
          {!hasAdditional && primary.sourceUrl && <SourceIconLink url={primary.sourceUrl}/>}
          <RequestMenu
            data={data}
            request={primary}
            customer={customer}
            variant={variant}
            topLevel
            hasAdditional={hasAdditional}
            onOpenChange={setMenuOpen}
            onEdit={() => { setExpanded(true); setEditingId(primary.id) }}
            onNewRequest={customer ? () => { setExpanded(true); setAdding(true) } : undefined}
            onChanged={onChanged}
            onDeleted={onDeleted}
          />
        </div>
      </div>
    </div>
    {open && <div className="customer-request-row__details">
      {adding && customer && <EmbeddedCustomerNeedForm
        data={data}
        host={variant}
        issueId={primary.issueId || undefined}
        projectId={primary.issueId ? undefined : primary.projectId}
        customer={customer}
        className="customer-request-row__new-form"
        onCancel={() => setAdding(false)}
        onCreated={request => { setAdding(false); onChanged?.(request) }}
      />}
      {[primary, ...additional].map(request => <NeedDetails
        key={request.id}
        data={data}
        request={request}
        customer={customer}
        variant={variant}
        withHeader={hasAdditional || adding}
        editing={editingId === request.id}
        issue={variant === 'projectPage' ? issueFor?.(request) : undefined}
        onEdit={() => setEditingId(request.id)}
        onEditDone={() => { setEditingId(null); if (singleEmpty) setExpanded(false) }}
        onChanged={onChanged}
        onDeleted={onDeleted}
      />)}
    </div>}
  </div>
}

function NeedDetails({ data, request, customer, variant, withHeader, editing, issue, onEdit, onEditDone, onChanged, onDeleted }: CustomerRequestRowCallbacks & {
  data: BootstrapData
  request: CustomerRequest
  customer?: Customer
  variant: EmbeddedCustomerNeedHost
  withHeader: boolean
  editing: boolean
  issue?: Issue
  onEdit: () => void
  onEditDone: () => void
}) {
  const { t } = useI18n()
  return <div className="customer-request-row__need" id={`${CUSTOMER_REQUEST_HASH}${request.id}`}>
    {withHeader && <div className="customer-request-row__need-header">
      <span className="customer-request-row__need-meta">
        <AddedBy request={request}/>
        <FlowTooltip label={requestTimestamp(request.createdAt)}><span className="customer-request-row__muted">{compactRelativeTime(request.createdAt)}</span></FlowTooltip>
        {isImportantRequest(request) && <span className="customer-request-row__important is-small" aria-label={t('Important')}><ImportantIcon size={14}/></span>}
      </span>
      <RequestMenu data={data} request={request} customer={customer} variant={variant} onEdit={onEdit} onChanged={onChanged} onDeleted={onDeleted}/>
    </div>}
    {editing
      ? <EmbeddedCustomerNeedForm data={data} host={variant} request={request} variant="inline" onCancel={onEditDone} onSaved={saved => { onEditDone(); onChanged?.(saved) }}/>
      : request.body.trim() && <MentionBody className="customer-request-row__body" body={request.body}/>}
    {!editing && (request.sourceUrl || request.attachments?.length > 0) && <div className="customer-request-row__sources">
      {request.sourceUrl && <a className="customer-request-row__source" href={request.sourceUrl} target="_blank" rel="noreferrer" onClick={event => event.stopPropagation()}>
        <SourceLinkIcon size={14}/><span data-i18n-ignore>{sourceHost(request.sourceUrl) ?? request.sourceUrl}</span>
      </a>}
      {request.attachments?.map(attachment => <a className="customer-request-row__source" key={attachment.id} href={attachment.url} target="_blank" rel="noreferrer" onClick={event => event.stopPropagation()}>
        <ViewGlyph icon="Attachment" color="currentColor"/><span data-i18n-ignore>{attachment.title}</span>
      </a>)}
    </div>}
    {issue && <AppLink className="customer-request-row__issue" href={issuePath(data.workspace.urlKey, issue)} onClick={event => event.stopPropagation()}>
      <StatusIcon state={issue.state} size={14}/><span className="customer-request-row__issue-id">{issue.identifier}</span><span data-i18n-ignore>{issue.title}</span>
    </AppLink>}
  </div>
}

function AddedBy({ request }: { request: CustomerRequest }) {
  const { t } = useI18n()
  const name = request.creator?.displayName || request.creator?.name
  if (!name) return null
  return <span className="customer-request-row__added-by"><span>{t('Added by')}&nbsp;</span><span className="customer-request-row__creator" data-i18n-ignore>{name}</span></span>
}

/** The row's customer: logo and name linking to the customer, or "Unknown customer" to pick one. */
function CustomerChip({ data, customer, request, onChanged }: { data: BootstrapData; customer?: Customer; request: CustomerRequest } & CustomerRequestRowCallbacks) {
  const { t } = useI18n()
  if (customer) return <AppLink className="customer-request-row__customer is-link" href={customerPath(data.workspace.urlKey, customer)} onClick={event => event.stopPropagation()}>
    <CustomerLogo customer={customer} size={16} withBackground={false}/>
    <span data-i18n-ignore>{customer.name}</span>
  </AppLink>
  return <DropdownMenu.Root>
    <FlowTooltip label={t('Add customer to request')}>
      <DropdownMenu.Trigger asChild>
        <button className="customer-request-row__customer is-unknown" type="button" onClick={event => event.stopPropagation()}>
          <CustomerLogo customer={null} size={16}/>
          <span>{t('Unknown customer')}</span>
        </button>
      </DropdownMenu.Trigger>
    </FlowTooltip>
    <LinearDropdownMenuContent label={t('Change customer…')} className="customer-request-menu">
      <ChangeCustomerOptions data={data} request={request} onChanged={onChanged}/>
    </LinearDropdownMenuContent>
  </DropdownMenu.Root>
}

function SourceIconLink({ url }: { url: string }) {
  const { t } = useI18n()
  return <FlowTooltip label={t('Open link')}>
    <a className="customer-request-row__icon-button" href={url} target="_blank" rel="noreferrer" aria-label={t('Open link')} onClick={event => event.stopPropagation()}><SourceLinkIcon size={14}/></a>
  </FlowTooltip>
}

function requestLink(data: BootstrapData, request: CustomerRequest) {
  const issue = request.issueId ? data.issues.find(item => item.id === request.issueId) : undefined
  const project = request.projectId ? data.projects.find(item => item.id === request.projectId) : undefined
  const path = issue ? issuePath(data.workspace.urlKey, issue) : project ? projectPath(data.workspace.urlKey, project, 'requests') : window.location.pathname
  return `${window.location.origin}${path}#${CUSTOMER_REQUEST_HASH}${request.id}`
}

async function copy(text: string, message: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast.success(message)
  } catch (error) {
    toast.error(error instanceof Error ? error.message : 'Could not write to clipboard')
  }
}

/**
 * The request's "…" menu (Linear's EmbeddedCustomerNeedRow actions): Mark as important, Move to…,
 * Copy, Create issue from request…, Edit request, New request from …, Change customer…, Open
 * customer and Delete. A row grouping several requests keeps the per-request actions on each one.
 */
function RequestMenu({ data, request, customer, variant, topLevel = false, hasAdditional = false, onOpenChange, onEdit, onNewRequest, onChanged, onDeleted }: CustomerRequestRowCallbacks & {
  data: BootstrapData
  request: CustomerRequest
  customer?: Customer
  variant: EmbeddedCustomerNeedHost
  topLevel?: boolean
  hasAdditional?: boolean
  onOpenChange?: (open: boolean) => void
  onEdit: () => void
  onNewRequest?: () => void
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const important = isImportantRequest(request)
  const perRequest = !topLevel || !hasAdditional
  const update = async (input: Parameters<typeof updateCustomerRequest>[1], success?: ReactNode) => {
    try {
      const saved = await updateCustomerRequest(request.id, input)
      onChanged?.(saved)
      if (success) toast.success(success)
      return saved
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Failed to save customer request'))
    }
  }
  const remove = async () => {
    const title = customer ? t('Delete customer request from {name}').replace('{name}', customer.name) : t('Delete customer request')
    if (!await confirmAction(title, { description: t('You cannot undo this action.'), confirmLabel: t('Delete'), danger: true })) return
    try {
      await deleteCustomerRequest(request.id)
      onDeleted?.(request.id)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Failed to delete customer request'))
    }
  }
  const createIssueFromRequest = async () => {
    const preferred = data.workspaceSettings.featureSettings?.customerDefaultTeamId
    const team = data.teams.find(item => item.id === preferred) ?? data.teams.find(item => !item.archivedAt) ?? data.teams[0]
    if (!team) return
    try {
      const issue = await createIssue({ title: customer?.name ? `${t('Customer request from')} ${customer.name}` : t('New issue'), description: request.body, teamId: team.id, projectId: request.projectId || undefined })
      await update({ issueId: issue.id }, `${t('Issue created')}: ${issue.identifier}`)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Failed to create issue'))
    }
  }
  return <DropdownMenu.Root open={open} onOpenChange={next => { setOpen(next); onOpenChange?.(next) }}>
    <DropdownMenu.Trigger asChild>
      <button className="customer-request-row__icon-button customer-request-row__menu" type="button" aria-label={t('Request options')} onClick={event => event.stopPropagation()}><MoreIcon size={16}/></button>
    </DropdownMenu.Trigger>
    <LinearDropdownMenuContent label={t('Request options')} className="customer-request-menu" align="end">
      <LinearMenuItem icon={important ? <NotImportantIcon/> : <ImportantIcon/>} label={important ? 'Not important' : 'Mark as important'} onSelect={() => void update({ priority: important ? 0 : 1 })}/>
      {perRequest && <LinearSubmenu icon={<LinearGlyph name="moveTo"/>} label="Move to…">
        <LinearSubmenu icon={<ViewGlyph icon="IssueStatusTodo" color="currentColor"/>} label="Move to issue…" search>
          <MoveToIssueOptions data={data} request={request} onMove={issue => void update({ issueId: issue.id }, `${t('Request moved to')} ${issue.identifier}`)}/>
        </LinearSubmenu>
        <LinearSubmenu icon={<ViewGlyph icon="Project" color="currentColor"/>} label="Move to project…" search>
          <LinearMenuOptions
            options={data.projects.filter(project => !project.archivedAt).map(project => ({ id: project.id, label: project.name, translate: false, icon: <ViewGlyph icon="Project" color={project.color || 'currentColor'}/> }))}
            selected={new Set(request.issueId ? [] : [request.projectId ?? ''])}
            placeholder="Search projects…"
            emptyLabel="No matching projects"
            onChoose={id => { const project = data.projects.find(item => item.id === id); if (project) void update({ projectId: project.id }, `${t('Request moved to')} ${project.name}`) }}
          />
        </LinearSubmenu>
      </LinearSubmenu>}
      {request.sourceUrl && perRequest
        ? <LinearSubmenu icon={<LinearGlyph name="copy"/>} label="Copy">
            <LinearMenuItem icon={<SourceLinkIcon/>} label="Copy link" onSelect={() => void copy(requestLink(data, request), t('URL copied to clipboard'))}/>
            <LinearMenuItem icon={<SourceLinkIcon/>} label="Copy source link" onSelect={() => void copy(request.sourceUrl!, t('URL copied to clipboard'))}/>
          </LinearSubmenu>
        : <LinearMenuItem icon={<SourceLinkIcon/>} label="Copy link" onSelect={() => void copy(requestLink(data, request), t('URL copied to clipboard'))}/>}
      {perRequest && <LinearMenuItem icon={<ViewGlyph icon="IssueStatusTodo" color="currentColor"/>} label="Create issue from request…" onSelect={() => void createIssueFromRequest()}/>}
      <LinearMenuSeparator/>
      {perRequest && <LinearMenuItem icon={<LinearGlyph name="edit"/>} label="Edit request" onSelect={onEdit}/>}
      {topLevel && onNewRequest && customer && <LinearMenuItem icon={<RequestPlusIcon/>} label={variant === 'customerPage' ? `${t('New request on')} ${t(request.issueId ? 'issue' : 'project')}` : t('New request from {name}').replace('{name}', customer.name)} translate={false} onSelect={onNewRequest}/>}
      {perRequest && <LinearSubmenu icon={null} label="Change customer…" search>
        <ChangeCustomerOptions data={data} request={request} onChanged={onChanged}/>
      </LinearSubmenu>}
      {customer && variant !== 'customerPage' && <LinearMenuItem icon={<IssueActionGlyph label="Add customer request…" fallback={null}/>} label="Open customer" href={customerPath(data.workspace.urlKey, customer)}/>}
      {perRequest && <>
        <LinearMenuSeparator/>
        <LinearMenuItem icon={<LinearGlyph name="delete"/>} label="Delete" onSelect={() => void remove()}/>
      </>}
    </LinearDropdownMenuContent>
  </DropdownMenu.Root>
}

/** "Change customer…": the customers (picking the current one removes it) and "Create new customer". */
function ChangeCustomerOptions({ data, request, onChanged }: { data: BootstrapData; request: CustomerRequest } & CustomerRequestRowCallbacks) {
  const { t } = useI18n()
  const options: LinearMenuOption[] = [...data.customers].sort((left, right) => left.name.localeCompare(right.name))
    .map(customer => ({ id: customer.id, label: customer.name, translate: false, keywords: customer.domains?.join(' '), icon: <CustomerLogo customer={customer} size={16}/> }))
  const change = async (input: { customerId?: string; customerName?: string }, name?: string) => {
    try {
      const saved = await updateCustomerRequest(request.id, input)
      onChanged?.(saved)
      toast.success(saved.customerId ? `${t('Customer changed to')} ${name ?? data.customers.find(item => item.id === saved.customerId)?.name ?? ''}` : t('Customer removed from request'))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Failed to save customer request'))
    }
  }
  return <LinearMenuOptions
    options={options}
    selected={new Set(request.customerId ? [request.customerId] : [])}
    placeholder="Search customers…"
    emptyLabel={data.customers.length ? 'No results' : 'Type a name to create your first customer'}
    onChoose={id => void change({ customerId: id === request.customerId ? '' : id })}
    footer={query => query ? <LinearMenuItem icon={<RequestPlusIcon/>} label={t('Create new customer: "{name}"').replace('{name}', query)} translate={false} onSelect={() => void change({ customerName: query }, query)}/> : null}
  />
}

function MoveToIssueOptions({ data, request, onMove }: { data: BootstrapData; request: CustomerRequest; onMove: (issue: Issue) => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const issues = useIssueSearch(query, data.issues)
  const text = query.trim().toLocaleLowerCase()
  const visible = issues.filter(issue => issue.id !== request.issueId && (!text || `${issue.identifier} ${issue.title}`.toLocaleLowerCase().includes(text))).slice(0, 50)
  return <>
    <LinearMenuSearch value={query} onChange={setQuery} placeholder="Search issues…"/>
    <div className="linear-menu__list" role="presentation">
      {visible.map(issue => <LinearMenuItem key={issue.id} icon={<StatusIcon state={issue.state} size={14}/>} label={`${issue.identifier} ${issue.title}`} translate={false} onSelect={() => onMove(issue)}/>)}
      {!visible.length && <div className="linear-menu__empty">{t('No matching issues')}</div>}
    </div>
  </>
}
