import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Paperclip } from 'lucide-react'
import { Fragment, useEffect, useMemo, useState, type ReactNode } from 'react'
import { toast } from 'sonner'

import { PriorityIcon, ProjectStatusIcon, StatusIcon } from '@/components/issue/issue-icons'
import { confirmAction, promptAction } from '@/components/ui/action-dialog-service'
import { AppLink } from '@/components/ui/app-link'
import { DisclosureTriangle } from '@/components/ui/disclosure-triangle'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { LinearContextMenuRoot, LinearContextMenuTrigger, LinearDropdownMenuContent, LinearMenuContent, LinearMenuItem, LinearMenuOptions, LinearMenuSearch, LinearMenuSeparator, LinearSubmenu } from '@/components/ui/row-context-menu'
import { FlowTooltip } from '@/components/ui/tooltip'
import { PlusIcon } from '@/components/ui/view-action-icons'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { createIssue, deleteCustomerRequest, deleteCustomerRequestAttachment, listIssueRecords, updateCustomerRequest } from '@/lib/api'
import { customerPath, issuePath, projectPath } from '@/lib/app-routes'
import type { BootstrapData, Customer, CustomerRequest, Issue, Project } from '@/types/flow'
import { CustomerNeedComposer } from './customer-need-composer'
import { CustomerLogo } from '@/components/customer/customer-logo'
import { MentionBody } from '@/components/editor/mentions/mention-body'
import { ChevronRightSmallIcon, CreateIssueIcon, CustomerDefaultLogoIcon, CustomerPageEmptyIcon, EditPencilIcon, ImportantIcon, IssueCircleIcon, LinkIcon, MoveToIcon, NotImportantIcon, RequestDetailsIcon, ArchiveBoxIcon } from './customer-page-icons'
import { customerRequestIssueState, customerRequestIssueTeam, fullTimestamp, groupHasDetails, groupHasImportant, isImportant, longRelativeTime, orderCustomerNeedGroups, passesCompletedWindow, sectionCustomerNeedGroups, shortRelativeTime, type CustomerNeedGroup, type CustomerPageViewPreferences } from './customer-page-model'
import { CustomerPageViewOptions } from './customer-page-view-options'

export const CUSTOMER_REQUEST_HASH_PREFIX = 'customerRequest-'
const DOCS_URL = 'https://flow.app/docs/customer-requests'

type RequestsProps = {
  data: BootstrapData
  customer: Customer
  groups: CustomerNeedGroup[]
  archivedCount: number
  showArchived: boolean
  onToggleShowArchived: () => void
  composerOpen: boolean
  onComposerOpenChange: (open: boolean) => void
  view: CustomerPageViewPreferences
  onViewChange: (view: CustomerPageViewPreferences) => void
  onReload: (issueIds?: string[]) => Promise<void>
  addShortcut: string
}

/**
 * Linear's customer page "Requests" section (CustomerPage.Un): heading with the request and
 * important counts, display options and "Add request"; the composer; one row per linked issue or
 * project; the empty state; and "Show N archived requests".
 */
export function CustomerPageRequests({ data, customer, groups, archivedCount, showArchived, onToggleShowArchived, composerOpen, onComposerOpenChange, view, onViewChange, onReload, addShortcut }: RequestsProps) {
  const { t } = useI18n()
  const [optionsOpen, setOptionsOpen] = useState(false)
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set())
  const [collapsedSections, setCollapsedSections] = useState<ReadonlySet<string>>(() => new Set())
  const visible = useMemo(() => groups.filter(group => passesCompletedWindow(group, view.completed)), [groups, view.completed])
  const ordered = useMemo(() => orderCustomerNeedGroups(visible, view, data), [visible, view, data])
  const sections = useMemo(() => sectionCustomerNeedGroups(ordered, view.grouping, data), [ordered, view.grouping, data])
  const importantCount = visible.filter(groupHasImportant).length
  const hiddenByDisplayOptions = groups.length - visible.length

  // A `#customerRequest-{id}` link opens and reveals that request (Linear highlights it).
  useEffect(() => {
    const id = decodeURIComponent(window.location.hash.replace(/^#/, ''))
    if (!id.startsWith(CUSTOMER_REQUEST_HASH_PREFIX)) return
    const requestId = id.slice(CUSTOMER_REQUEST_HASH_PREFIX.length)
    const group = groups.find(item => item.primary.id === requestId || item.additional.some(request => request.id === requestId))
    if (!group) return
    setExpanded(current => new Set([...current, group.key]))
    requestAnimationFrame(() => document.querySelector(`[data-customer-need="${CSS.escape(group.key)}"]`)?.scrollIntoView({ block: 'center' }))
  }, [groups.length]) // eslint-disable-line react-hooks/exhaustive-deps

  const toggle = (key: string, open?: boolean) => setExpanded(current => {
    const next = new Set(current)
    if (open ?? !next.has(key)) next.add(key)
    else next.delete(key)
    return next
  })

  return <section className="customer-needs" aria-label={t('Requests')}>
    <div className="customer-needs__header">
      <div className="customer-needs__heading">
        <span className="customer-needs__title">{t('Requests')}</span>
        <span className="customer-needs__count">{visible.length}</span>
        {importantCount > 0 && <FlowTooltip label={t(importantCount === 1 ? '1 important request' : '{count} important requests').replace('{count}', String(importantCount))}>
          <span className="customer-needs__important"><ImportantIcon size={16}/>{importantCount}</span>
        </FlowTooltip>}
      </div>
      <div className="customer-needs__actions">
        {groups.length > 0 && <CustomerPageViewOptions view={view} onChange={onViewChange} open={optionsOpen} onOpenChange={setOptionsOpen}/>}
        <FlowTooltip label={t('Add customer request')} shortcut={addShortcut}>
          <button type="button" className="customer-needs__add" onClick={() => onComposerOpenChange(true)}><PlusIcon/>{t('Add request')}</button>
        </FlowTooltip>
      </div>
    </div>
    {composerOpen && <div className="customer-needs__composer">
      <CustomerNeedComposer data={data} customer={customer} mode="customer" onClose={() => onComposerOpenChange(false)} onSaved={issueIds => onReload(issueIds)}/>
    </div>}
    {ordered.length > 0
      ? <div className="customer-needs__list" role="list">
        {sections.map((section, sectionIndex) => {
          const collapsed = collapsedSections.has(section.id)
          return <Fragment key={section.id}>
            {section.title && <div className="customer-needs__group" data-first={sectionIndex === 0 || undefined} onDoubleClick={() => setCollapsedSections(current => toggleSet(current, section.id))}>
              <button type="button" className="customer-needs__group-toggle" aria-label={t(collapsed ? 'Expand group' : 'Collapse group')} aria-expanded={!collapsed} onClick={() => setCollapsedSections(current => toggleSet(current, section.id))}><DisclosureTriangle open={!collapsed}/></button>
              <span className="customer-needs__group-title">{t(section.title)}</span>
              <span className="customer-needs__group-count">{section.groups.length}</span>
              <span aria-hidden="true" className="customer-needs__group-rule"/>
            </div>}
            {!collapsed && section.groups.map(group => <CustomerNeedRow key={group.key} data={data} customer={customer} group={group} view={view} expanded={expanded.has(group.key)} onToggle={open => toggle(group.key, open)} onReload={onReload}/>)}
          </Fragment>
        })}
      </div>
      : !composerOpen && <div className="customer-needs__empty">
        <div className="customer-needs__empty-icon"><CustomerPageEmptyIcon height={100}/></div>
        <div className="customer-needs__empty-text">
          <strong>{t('Customer requests')}</strong>
          <p>{t(archivedCount > 0 && !showArchived ? 'All customer requests for this customer are archived. Create a new request or show the archived ones.' : 'No customer requests created yet. Use a supported integration to automatically create requests, or create one manually.')}</p>
        </div>
        <div className="customer-needs__empty-actions">
          <button type="button" className="customer-needs__primary" onClick={() => onComposerOpenChange(true)}>{t('Add request')}<span className="customer-needs__keys" aria-hidden="true">{addShortcut.split(' ').map(key => <kbd key={key}>{key}</kbd>)}</span></button>
          <a className="customer-needs__secondary" href={DOCS_URL} rel="noreferrer" target="_blank">{t('Documentation')}</a>
        </div>
      </div>}
    {hiddenByDisplayOptions > 0 && <div className="customer-needs__hidden" data-has-rows={ordered.length > 0 || undefined}>
      <span>{t(hiddenByDisplayOptions === 1 ? '1 request hidden by display options' : '{count} requests hidden by display options').replace('{count}', String(hiddenByDisplayOptions))}</span>
      <button type="button" onClick={() => setOptionsOpen(true)}>{t('Show options')}</button>
    </div>}
    {archivedCount > 0 && !showArchived && <div className="customer-needs__archived" data-after-hidden={hiddenByDisplayOptions > 0 || undefined}>
      <button type="button" onClick={onToggleShowArchived}><ArchiveBoxIcon size={14}/>{t(archivedCount === 1 ? 'Show 1 archived request' : 'Show {count} archived requests').replace('{count}', String(archivedCount))}</button>
    </div>}
  </section>
}

function toggleSet(current: ReadonlySet<string>, key: string) {
  const next = new Set(current)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  return next
}

type RowProps = {
  data: BootstrapData
  customer: Customer
  group: CustomerNeedGroup
  view: CustomerPageViewPreferences
  expanded: boolean
  onToggle: (open?: boolean) => void
  onReload: (issueIds?: string[]) => Promise<void>
}

/** One request row (CustomerPage.Pn): the issue/project, badges and time; expands into the requests. */
function CustomerNeedRow({ data, customer, group, view, expanded, onToggle, onReload }: RowProps) {
  const { t, locale } = useI18n()
  const [menuOpen, setMenuOpen] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const { primary, additional, issue, project } = group
  const needs = [primary, ...additional]
  const hasAdditional = additional.length > 0
  const singleEmpty = !primary.body.trim() && !hasAdditional && !primary.attachments.length
  const archived = needs.every(need => need.archivedAt) || Boolean(issue?.archivedAt || project?.archivedAt)
  const featureHref = issue ? issuePath(data.workspace.urlKey, issue) : project ? projectPath(data.workspace.urlKey, project) : undefined
  const reloadIssues = () => onReload(issue ? [issue.id] : undefined)
  const editNeed = (id: string) => { onToggle(true); setMenuOpen(false); setEditingId(id) }
  const actions = useNeedActions({ data, customer, group, onReload, onEdit: editNeed, onNewRequest: () => { setCreateOpen(true); onToggle(true) } })
  const openRow = () => {
    if (singleEmpty && !createOpen) { editNeed(primary.id); return }
    onToggle()
  }
  const dueLabel = view.fieldTargetDueDate ? dueOrTarget(issue, project, locale) : undefined
  const title = issue ? issue.title : project ? project.name : group.pending ? t('Loading…') : t('Unlinked request')
  const indent = view.fieldIdentifier || view.fieldPriority ? 4 : 26

  const topRow = <div className="customer-need__row" role="button" tabIndex={0} aria-expanded={expanded} data-menu-open={menuOpen || undefined} onClick={openRow} onKeyDown={event => {
    if (event.target !== event.currentTarget || event.key !== 'Enter') return
    event.preventDefault()
    openRow()
  }}>
    <span className="customer-need__feature">
      {view.fieldPriority && (issue || project) && <PriorityIcon priority={(issue ?? project)!.priority} size={16}/>}
      {view.fieldIdentifier && issue && <span className="customer-need__identifier">{issue.identifier}</span>}
      {view.fieldStatus && issue && <StatusIcon state={issue.state} size={14}/>}
      {view.fieldStatus && project && <ProjectStatusIcon type={project.status.type} name={project.status.name} color={project.status.color} progress={project.progress} size={14}/>}
      {project && <ViewGlyph color={project.color} icon={project.icon}/>}
      {!issue && !project && <IssueCircleIcon size={14} className="customer-need__muted-icon"/>}
      <span className="customer-need__title" data-i18n-ignore={issue || project ? '' : undefined}>{title}</span>
      {groupHasDetails(group) && <FlowTooltip label={t('Request with details')}><span className="customer-need__details-icon"><RequestDetailsIcon size={16}/></span></FlowTooltip>}
      {featureHref && <FlowTooltip label={t(issue ? 'Open issue' : 'Open project')}>
        <AppLink className="customer-need__open" aria-label={t(issue ? 'Open issue' : 'Open project')} href={featureHref} tabIndex={-1} onClick={event => event.stopPropagation()}><ChevronRightSmallIcon size={14}/></AppLink>
      </FlowTooltip>}
    </span>
    <span className="customer-need__info">
      {dueLabel && <FlowTooltip label={t(issue ? 'Due date' : 'Target date')}><span className="customer-need__date-pill">{dueLabel}</span></FlowTooltip>}
      {archived && <FlowTooltip label={t(issue ? 'Issue is archived' : 'Project is archived')}><span className="customer-need__archived">{t('Archived')}</span></FlowTooltip>}
      {hasAdditional && <FlowTooltip label={t('Number of requests')}><span className="customer-need__badge-slot"><span className="customer-need__count">{needs.length}</span></span></FlowTooltip>}
      {groupHasImportant(group) && <FlowTooltip label={t('Important')}><span className="customer-need__badge-slot customer-need__important"><ImportantIcon size={16}/></span></FlowTooltip>}
      <span className="customer-need__end" data-expanded={expanded || undefined}>
        <FlowTooltip label={t('Created at {date}').replace('{date}', fullTimestamp(primary.createdAt, locale))}>
          <span className="customer-need__time">{localizedShortTime(primary.createdAt, t)}</span>
        </FlowTooltip>
        <span className="customer-need__menu-slot">
          <DropdownMenu.Root open={menuOpen} onOpenChange={setMenuOpen}>
            <DropdownMenu.Trigger asChild>
              <button type="button" className="customer-need__menu" aria-label={t('Customer request actions')} onClick={event => event.stopPropagation()}><DotsIcon/></button>
            </DropdownMenu.Trigger>
            {menuOpen && <LinearDropdownMenuContent label={t('Customer request actions')} align="end">{actions.top}</LinearDropdownMenuContent>}
          </DropdownMenu.Root>
        </span>
      </span>
    </span>
  </div>

  return <div className="customer-need" role="listitem" data-customer-need={group.key} data-expanded={expanded || undefined} data-active={expanded || menuOpen || undefined}>
    <LinearContextMenuRoot onOpenChange={setMenuOpen}>
      <LinearContextMenuTrigger asChild>{topRow}</LinearContextMenuTrigger>
      <LinearMenuContent label={t('Customer request actions')}>{actions.top}</LinearMenuContent>
    </LinearContextMenuRoot>
    {expanded && <div className="customer-need__expanded" style={{ paddingLeft: indent }}>
      {createOpen && <CustomerNeedComposer className="customer-need__inline-form" data={data} customer={customer} mode="feature" issue={issue} project={project} onClose={() => setCreateOpen(false)} onSaved={() => reloadIssues()}/>}
      <div className="customer-need__needs">
        {needs.map(need => <NeedDetail key={need.id} data={data} customer={customer} group={group} need={need} editing={editingId === need.id} onEdit={() => editNeed(need.id)} onCloseEdit={() => { setEditingId(null); if (singleEmpty) onToggle(false) }} onReload={onReload}/>)}
      </div>
    </div>}
  </div>
}

/** Linear's short row time (`3d`) through the translation templates (`{count}d`). */
function localizedShortTime(value: string, t: (source: string) => string) {
  const short = shortRelativeTime(value)
  const match = /^(\d+)(\D+)$/.exec(short)
  return match ? t(`{count}${match[2]}`).replace('{count}', match[1]) : t(short)
}

function localizedLongTime(value: string, t: (source: string) => string) {
  const long = longRelativeTime(value)
  const match = /^(\d+) /.exec(long)
  return match && match[1] !== '1' ? t(long.replace(/^\d+/, '{count}')).replace('{count}', match[1]) : t(long)
}

function DotsIcon() {
  return <svg aria-hidden="true" width="16" height="16" viewBox="0 0 16 16" fill="currentColor"><path d="M3 6.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Z"/></svg>
}

function dueOrTarget(issue: Issue | undefined, project: Project | undefined, locale: string) {
  const value = issue ? issue.dueDate : project?.targetDate
  if (!value) return undefined
  return new Intl.DateTimeFormat(locale, { month: 'short', day: 'numeric' }).format(new Date(value.length === 10 ? `${value}T00:00:00` : value))
}

/** One request inside an expanded row (CustomerPage.Fn): "Added by", time, body or edit form, files. */
function NeedDetail({ data, customer, group, need, editing, onEdit, onCloseEdit, onReload }: { data: BootstrapData; customer: Customer; group: CustomerNeedGroup; need: CustomerRequest; editing: boolean; onEdit: () => void; onCloseEdit: () => void; onReload: (issueIds?: string[]) => Promise<void> }) {
  const { t, locale } = useI18n()
  const [menuOpen, setMenuOpen] = useState(false)
  const hasAdditional = group.additional.length > 0
  const actions = useNeedActions({ data, customer, group, need, onReload, onEdit })
  const creator = need.creator
  return <div className="customer-need-detail" data-important={isImportant(need) || undefined}>
    <div className="customer-need-detail__header">
      <span className="customer-need-detail__meta">
        {creator && <span className="customer-need-detail__creator">{t('Added by')}&nbsp;<strong data-i18n-ignore>{creator.displayName || creator.name}</strong>&nbsp;</span>}
        <FlowTooltip label={fullTimestamp(need.createdAt, locale)}><span className="customer-need-detail__time">{localizedLongTime(need.createdAt, t)}</span></FlowTooltip>
        {need.sourceUrl && <FlowTooltip label={t('Open link')}><a className="customer-need-detail__source" aria-label={t('Open link')} href={need.sourceUrl} rel="noreferrer" target="_blank" onClick={event => event.stopPropagation()}><LinkIcon size={14}/></a></FlowTooltip>}
      </span>
      {hasAdditional && <DropdownMenu.Root open={menuOpen} onOpenChange={setMenuOpen}>
        <DropdownMenu.Trigger asChild><button type="button" className="customer-need__menu" aria-label={t('Customer request actions')}><DotsIcon/></button></DropdownMenu.Trigger>
        {menuOpen && <LinearDropdownMenuContent label={t('Customer request actions')} align="end">{actions.single}</LinearDropdownMenuContent>}
      </DropdownMenu.Root>}
    </div>
    {editing
      ? <CustomerNeedComposer className="customer-need__inline-form" data={data} customer={customer} mode="edit" request={need} onClose={onCloseEdit} onSaved={() => onReload(need.issueId ? [need.issueId] : undefined)}/>
      : need.body.trim() && <div className="customer-need-detail__body" onDoubleClick={onEdit}><MentionBody body={need.body}/></div>}
    {need.attachments.length > 0 && <div className="customer-need-detail__files">
      {need.attachments.map(attachment => <span key={attachment.id}>
        <a href={attachment.url} target="_blank" rel="noreferrer"><Paperclip size={12} aria-hidden="true"/><span data-i18n-ignore>{attachment.title}</span></a>
        <button type="button" aria-label={t('Remove {name}').replace('{name}', attachment.title)} onClick={() => void deleteCustomerRequestAttachment(need.id, attachment.id).then(() => onReload())}>×</button>
      </span>)}
    </div>}
  </div>
}

type ActionInput = { data: BootstrapData; customer: Customer; group: CustomerNeedGroup; need?: CustomerRequest; onReload: (issueIds?: string[]) => Promise<void>; onEdit: (id: string) => void; onNewRequest?: () => void }

/**
 * The request menus (AdditionalCustomerNeedCreateForm.Ye, customer page variant): the row menu, and
 * — when a row holds several requests — the menu of each request.
 */
function useNeedActions({ data, customer, group, need, onReload, onEdit, onNewRequest }: ActionInput): { top: ReactNode; single: ReactNode } {
  const { t } = useI18n()
  const { primary, additional, issue, project } = group
  const hasAdditional = additional.length > 0
  const target = need ?? primary
  const needs = need ? [need] : [primary, ...additional]
  const important = needs.some(isImportant)
  const featureLabel = issue ? 'issue' : 'project'
  const workspace = data.workspace.urlKey
  const touched = (request: CustomerRequest, issueId?: string) => [request.issueId, issueId].filter((id): id is string => Boolean(id))

  const run = async (work: () => Promise<unknown>, success?: string) => {
    try { await work(); if (success) toast.success(success) }
    catch (error) { toast.error(error instanceof Error ? error.message : t('Something went wrong')) }
  }
  const toggleImportant = () => void run(async () => {
    await Promise.all(needs.map(request => updateCustomerRequest(request.id, { priority: important ? 0 : 1 })))
    await onReload(issue ? [issue.id] : undefined)
  })
  const copy = (value: string, message: string) => void navigator.clipboard.writeText(value).then(() => toast.success(t(message))).catch(() => toast.error(t('Could not copy to clipboard')))
  const requestUrl = `${window.location.origin}${customerPath(workspace, customer)}#${CUSTOMER_REQUEST_HASH_PREFIX}${target.id}`
  const moveToIssue = (next: Issue) => void run(async () => {
    await updateCustomerRequest(target.id, { issueId: next.id, projectId: '' })
    toast.success(t('Customer request moved to {target}').replace('{target}', `${next.identifier} ${next.title}`))
    await onReload(touched(target, next.id))
  })
  const moveToProject = (next: Project) => void run(async () => {
    await updateCustomerRequest(target.id, { issueId: '', projectId: next.id })
    toast.success(t('Customer request moved to {target}').replace('{target}', next.name))
    await onReload(touched(target))
  })
  const changeCustomer = (customerId: string) => {
    const next = data.customers.find(item => item.id === customerId)
    if (!next || next.id === target.customerId) return
    void run(async () => {
      await updateCustomerRequest(target.id, { customerId: next.id })
      toast.success(t('Customer changed to {customer}').replace('{customer}', next.name))
      await onReload(touched(target))
    })
  }
  const createIssueFromRequest = () => void (async () => {
    const title = await promptAction(t('Create issue from request…'), target.body.split('\n')[0].trim().slice(0, 255) || `Customer request from ${customer.name}`, { confirmLabel: t('Create issue') })
    if (!title?.trim()) return
    await run(async () => {
      const team = customerRequestIssueTeam(data)
      if (!team) throw new Error(t('Create a team before adding requests'))
      const created = await createIssue({ title: title.trim(), description: target.body, teamId: team.id, stateId: customerRequestIssueState(data, team.id)?.id, priority: 0, projectId: target.projectId || undefined })
      await updateCustomerRequest(target.id, { issueId: created.id, projectId: '' })
      toast.success(t('Request from {customer} added to {target}').replace('{customer}', customer.name).replace('{target}', `${created.identifier} ${created.title}`))
      await onReload(touched(target, created.id))
    })
  })()
  const remove = () => void (async () => {
    const confirmed = await confirmAction(t('Delete customer request from {customer}').replace('{customer}', customer.name), { description: t('You cannot undo this action.'), confirmLabel: t('Delete'), danger: true })
    if (!confirmed) return
    await run(async () => {
      await deleteCustomerRequest(target.id)
      await onReload(touched(target))
    })
  })()
  const openFeature = issue ? issuePath(workspace, issue) : project ? projectPath(workspace, project) : undefined

  const importantItem = <LinearMenuItem icon={important ? <NotImportantIcon/> : <ImportantIcon/>} label={important ? 'Not important' : 'Mark as important'} onSelect={toggleImportant}/>
  const copyItems = target.sourceUrl
    ? <LinearSubmenu icon={<LinearGlyph name="copy"/>} label="Copy">
      <LinearMenuItem icon={<LinkIcon/>} label="Copy link" onSelect={() => copy(requestUrl, 'URL copied to clipboard')}/>
      <LinearMenuItem icon={<LinkIcon/>} label="Copy source link" onSelect={() => copy(target.sourceUrl!, 'Source URL copied to clipboard')}/>
    </LinearSubmenu>
    : <LinearMenuItem icon={<LinkIcon/>} label="Copy link" onSelect={() => copy(requestUrl, 'URL copied to clipboard')}/>
  const moveItem = <LinearSubmenu icon={<MoveToIcon/>} label="Move to…">
    <LinearSubmenu icon={<IssueCircleIcon/>} label="Move to issue…" search>{() => <IssueSearch exclude={issue?.id} onChoose={moveToIssue}/>}</LinearSubmenu>
    <LinearSubmenu icon={<LinearGlyph name="projectStatus"/>} label="Move to project…" search>
      <LinearMenuOptions placeholder="Search projects…" selected={new Set(project ? [project.id] : [])} options={data.projects.filter(item => !item.archivedAt).map(item => ({ id: item.id, label: item.name, translate: false, icon: <ViewGlyph color={item.color} icon={item.icon}/> }))} onChoose={id => { const next = data.projects.find(item => item.id === id); if (next) moveToProject(next) }}/>
    </LinearSubmenu>
  </LinearSubmenu>
  const createIssueItem = <LinearMenuItem icon={<CreateIssueIcon/>} label="Create issue from request…" onSelect={createIssueFromRequest}/>
  const editItem = <LinearMenuItem icon={<EditPencilIcon/>} label="Edit request" onSelect={() => onEdit(target.id)}/>
  const changeCustomerItem = <LinearSubmenu icon={<CustomerDefaultLogoIcon/>} label="Change customer…" search>
    <LinearMenuOptions placeholder="Search customers…" selected={new Set([target.customerId])} options={data.customers.map(item => ({ id: item.id, label: item.name, translate: false, icon: <CustomerLogo customer={item} size={16}/> }))} onChoose={changeCustomer}/>
  </LinearSubmenu>
  const openItem = openFeature ? <LinearMenuItem icon={issue ? <IssueCircleIcon/> : <LinearGlyph name="projectStatus"/>} label={issue ? 'Open issue' : 'Open project'} href={openFeature}/> : null
  const deleteItem = <LinearMenuItem icon={<LinearGlyph name="delete"/>} label="Delete" onSelect={remove}/>
  const newRequestItem = onNewRequest && (issue || project) ? <LinearMenuItem icon={<PlusIcon/>} label={`New request on ${featureLabel}`} onSelect={onNewRequest}/> : null

  const top = hasAdditional && !need
    ? <>{importantItem}<LinearMenuItem icon={<LinkIcon/>} label="Copy link" onSelect={() => copy(requestUrl, 'URL copied to clipboard')}/>{newRequestItem}{openItem}</>
    : <>{importantItem}{moveItem}{copyItems}{createIssueItem}<LinearMenuSeparator/>{editItem}{newRequestItem}{changeCustomerItem}{openItem}<LinearMenuSeparator/>{deleteItem}</>
  const single = <>{moveItem}{copyItems}{createIssueItem}<LinearMenuSeparator/>{editItem}{changeCustomerItem}{openItem}<LinearMenuSeparator/>{deleteItem}</>
  return { top, single }
}

/** "Move to issue…": searches issues on the server (paged workspaces keep only some issues loaded). */
function IssueSearch({ exclude, onChoose }: { exclude?: string; onChoose: (issue: Issue) => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<Issue[]>([])
  useEffect(() => {
    const controller = new AbortController()
    const timer = window.setTimeout(() => {
      listIssueRecords({ q: query.trim() || undefined, archived: 'false', limit: 10, sort: 'updatedAt', direction: 'desc' }, controller.signal)
        .then(page => setResults(page.items.filter(item => item.id !== exclude)))
        .catch(() => undefined)
    }, query ? 150 : 0)
    return () => { window.clearTimeout(timer); controller.abort() }
  }, [query, exclude])
  return <>
    <LinearMenuSearch value={query} onChange={setQuery} placeholder="Search issues…"/>
    <div className="linear-menu__list">
      {results.map(item => <LinearMenuItem key={item.id} icon={<StatusIcon state={item.state} size={14}/>} label={`${item.identifier} ${item.title}`} translate={false} onSelect={() => onChoose(item)}/>)}
      {!results.length && <div className="linear-menu__empty">{t('No results')}</div>}
    </div>
  </>
}
