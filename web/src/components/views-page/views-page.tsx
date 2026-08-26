import { useMemo, useState, type ReactNode } from 'react'
import * as ContextMenu from '@radix-ui/react-context-menu'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import * as Select from '@radix-ui/react-select'
import { ArrowDown, ArrowUp, Bell, BellOff, Building2, Check, ChevronRight, Copy, Ellipsis, Layers3, LockKeyhole, Pencil, Star, Trash2, UserRound } from 'lucide-react'
import { TeamIcon } from '@/components/issue/issue-icons'
import { toast } from 'sonner'
import type { BootstrapData, SavedView, SavedViewMutationInput, Subscription, Team, User } from '@/types/flow'
import type { ViewsResource } from '@/lib/app-routes'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { ViewsEmptyState } from './views-empty-state'
import styles from './views-page.module.css'
import { UserAvatar } from '@/components/ui/user-avatar'
import { CheckboxMark } from '@/components/ui/checkbox-mark'

type ViewsScope = { kind: 'workspace' } | { kind: 'team'; team: Team }
type Ordering = 'created' | 'name' | 'owner' | 'updated'
type Direction = 'asc' | 'desc'
type DisplayProperty = 'created' | 'owner' | 'updated'

export type ViewsPageProps = {
  data: BootstrapData
  resource: ViewsResource
  scope: ViewsScope
  views: SavedView[]
  onCreate: () => void
  onDelete: (view: SavedView) => Promise<void>
  onDuplicate: (view: SavedView) => void
  onEdit: (view: SavedView) => void
  onOpen: (view: SavedView) => void
  onOpenSidebar?: () => void
  onResourceChange: (resource: ViewsResource) => void
  resourceHref: (resource: ViewsResource) => string
  onUpdate: (viewId: string, input: SavedViewMutationInput) => Promise<SavedView>
  onToggleFavorite: (view: SavedView) => Promise<void>
  onSetSubscriptionEvents: (view: SavedView, events: string[]) => Promise<void>
  viewHref: (view: SavedView) => string
}

export function ViewsPage({ data, resource, scope, views, onCreate, onDelete, onDuplicate, onEdit, onOpen, onOpenSidebar, onResourceChange, resourceHref, onUpdate, onToggleFavorite, onSetSubscriptionEvents, viewHref }: ViewsPageProps) {
  const storageKey = `${data.workspace.urlKey}:views-directory:${scope.kind === 'team' ? scope.team.id : 'workspace'}:${resource}`
  const [ordering, setOrdering] = useState<Ordering>(() => readPreference(`${storageKey}:ordering`, scope.kind === 'team' ? 'owner' : 'name') as Ordering)
  const [direction, setDirection] = useState<Direction>(() => readPreference(`${storageKey}:direction`, 'asc') as Direction)
  const [properties, setProperties] = useState<Set<DisplayProperty>>(() => new Set(readProperties(`${storageKey}:properties`)))
  const [teamFavorite, setTeamFavorite] = useState(() => readPreference(`${storageKey}:favorite`, 'false') === 'true')
  const usersById = useMemo(() => new Map(data.users.map(user => [user.id, user])), [data.users])
  const orderedViews = useMemo(() => [...views].sort((left, right) => compareViews(left, right, ordering, direction, usersById, data.viewer)), [data.viewer, direction, ordering, usersById, views])
  const groups = useMemo(() => scope.kind === 'team'
    ? [{ id: scope.team.id, kind: 'team' as const, label: scope.team.name, views: orderedViews }]
    : ([
      { id: 'personal', kind: 'personal' as const, label: 'Personal views', views: orderedViews.filter(view => view.scope === 'personal') },
      { id: 'workspace', kind: 'workspace' as const, label: data.workspace.name, views: orderedViews.filter(view => view.scope !== 'personal') },
    ]).filter(group => group.views.length), [data.workspace.name, orderedViews, scope])

  const updateOrdering = (next: Ordering) => {
    const nextDirection = ordering === next ? (direction === 'asc' ? 'desc' : 'asc') : 'asc'
    setOrdering(next); setDirection(nextDirection)
    writePreference(`${storageKey}:ordering`, next); writePreference(`${storageKey}:direction`, nextDirection)
  }
  const changeDirection = () => {
    const next = direction === 'asc' ? 'desc' : 'asc'
    setDirection(next); writePreference(`${storageKey}:direction`, next)
  }
  const toggleProperty = (property: DisplayProperty) => {
    setProperties(current => {
      const next = new Set(current)
      if (next.has(property)) next.delete(property); else next.add(property)
      writePreference(`${storageKey}:properties`, JSON.stringify([...next]))
      return next
    })
  }
  const moveView = (view: SavedView, destination: SavedViewMutationInput) => onUpdate(view.id, { ...destination, teamId: destination.scope === 'team' ? destination.teamId : '' })
  const copyLink = async (view: SavedView) => {
    await navigator.clipboard.writeText(`${window.location.origin}${viewHref(view)}`)
    toast.success('View link copied')
  }
  const deleteView = async (view: SavedView) => {
    if (!window.confirm(`Delete view "${view.name}"? This cannot be undone.`)) return
    await onDelete(view)
  }

  return <div className={styles.page}>
    <header className={styles.header}>
      <button className={styles.mobileMenu} aria-label="Open workspace sidebar" onClick={onOpenSidebar} type="button">☰</button>
      <h2>Views</h2>
      {scope.kind === 'team' && <button aria-checked={teamFavorite} aria-label={teamFavorite ? 'Remove from favorites' : 'Add to favorites'} className={styles.favoriteHeader} onClick={() => { const next = !teamFavorite; setTeamFavorite(next); writePreference(`${storageKey}:favorite`, String(next)) }} role="switch" type="button"><ViewFavoriteIcon selected={teamFavorite}/></button>}
      <button aria-label="Create new view" className={styles.createHeader} onClick={onCreate} type="button"><FlowPlusIcon/><span>New view</span></button>
    </header>
    <div className={styles.toolbar}>
      <nav aria-label="View resources" className={styles.tabs}>
        {(['issues', 'projects'] as const).map(item => <a aria-current={resource === item ? 'page' : undefined} href={resourceHref(item)} key={item} onClick={event => { event.preventDefault(); onResourceChange(item) }}>{item === 'issues' ? 'Issues' : 'Projects'}</a>)}
      </nav>
      <ViewsDisplayMenu direction={direction} ordering={ordering} properties={properties} onDirection={changeDirection} onOrdering={next => { setOrdering(next); writePreference(`${storageKey}:ordering`, next) }} onToggleProperty={toggleProperty}/>
    </div>
    {orderedViews.length > 0 && <div className={styles.columns} style={{ '--views-columns': columnTemplate(properties) } as React.CSSProperties}>
      <SortButton active={ordering === 'name'} direction={direction} label="Name" onClick={() => updateOrdering('name')}/>
      {properties.has('created') && <SortButton active={ordering === 'created'} direction={direction} label="Created" onClick={() => updateOrdering('created')}/>} 
      {properties.has('updated') && <SortButton active={ordering === 'updated'} direction={direction} label="Updated" onClick={() => updateOrdering('updated')}/>} 
      {properties.has('owner') && <SortButton active={ordering === 'owner'} direction={direction} label="Owner" onClick={() => updateOrdering('owner')}/>} 
    </div>}
    <section className={`${styles.content} ${!orderedViews.length ? styles.contentEmpty : ''}`}>
      {orderedViews.length > 0 && groups.map(group => <div className={styles.group} key={group.id}>
        {scope.kind === 'workspace' && <div className={styles.groupHeader}>
          <ScopeAvatar kind={group.kind} label={group.label}/>
          <strong data-i18n-ignore>{group.label}</strong>
          <span>{group.kind === 'personal' ? '· Only visible to you' : '· Workspace'}</span>
          <button aria-label={`Create ${resource === 'issues' ? 'issue' : 'project'} view in ${group.label}`} onClick={onCreate} type="button"><FlowPlusIcon/></button>
        </div>}
        {group.views.map(view => <ViewRow data={data} favorite={view.favorite || data.favorites.some(item => item.userId === data.viewer.id && item.resourceType === 'view' && item.resourceId === view.id)} href={viewHref(view)} key={view.id} onCopy={() => { void copyLink(view) }} onDelete={() => { void deleteView(view) }} onDuplicate={() => onDuplicate(view)} onEdit={() => onEdit(view)} onMove={destination => { void moveView(view, destination) }} onOpen={() => onOpen(view)} onSetSubscriptionEvents={events => { void onSetSubscriptionEvents(view, events) }} onToggleFavorite={() => { void onToggleFavorite(view) }} onUpdate={input => { void onUpdate(view.id, input) }} properties={properties} resource={resource} subscribed={view.subscribed || data.subscriptions.some(item => item.userId === data.viewer.id && item.resourceType === 'view' && item.resourceId === view.id)} subscription={data.subscriptions.find(item => item.userId === data.viewer.id && item.resourceType === 'view' && item.resourceId === view.id)} usersById={usersById} view={view}/>) }
      </div>)}
      {!orderedViews.length && <ViewsEmptyState onCreate={onCreate} resource={resource}/>} 
    </section>
  </div>
}

function ViewRow({ data, favorite, href, onCopy, onDelete, onDuplicate, onEdit, onMove, onOpen, onSetSubscriptionEvents, onToggleFavorite, onUpdate, properties, resource, subscribed, subscription, usersById, view }: { data: BootstrapData; favorite: boolean; href: string; onCopy: () => void; onDelete: () => void; onDuplicate: () => void; onEdit: () => void; onMove: (input: SavedViewMutationInput) => void; onOpen: () => void; onSetSubscriptionEvents: (events: string[]) => void; onToggleFavorite: () => void; onUpdate: (input: SavedViewMutationInput) => void; properties: Set<DisplayProperty>; resource: ViewsResource; subscribed: boolean; subscription?: Subscription; usersById: Map<string, User>; view: SavedView }) {
  const owner = usersById.get(view.ownerId ?? '') ?? data.viewer
  const subscriptionEvents = new Set(subscription?.events?.length ? subscription.events : subscribed ? ['issue-added', 'issue-completed'] : [])
  const toggleSubscriptionEvent = (event: string) => {
    const next = new Set(subscriptionEvents)
    if (next.has(event)) next.delete(event); else next.add(event)
    onSetSubscriptionEvents([...next])
  }
  const row = <a aria-label={`${view.name} ${viewDescription(view, resource)} ${owner.displayName}`} className={styles.row} href={href} onClick={event => { event.preventDefault(); onOpen() }} onKeyDown={event => { if (event.key === ' ') { event.preventDefault(); onOpen() } }} style={{ '--views-columns': columnTemplate(properties) } as React.CSSProperties}>
    <div className={styles.identity}><ViewGlyph className={styles.viewIcon} color={view.color} icon={view.icon}/><span data-i18n-ignore><strong>{view.name}</strong><small>{viewDescription(view, resource)}</small></span>{favorite && <Star className={styles.rowFavorite} fill="currentColor" size={11}/>}</div>
    {properties.has('created') && <time>{formatDate(view.createdAt)}</time>}
    {properties.has('updated') && <time>{formatDate(view.updatedAt)}</time>}
    {properties.has('owner') && <OwnerMenu owner={owner} users={data.users} onChange={ownerId => onUpdate({ ownerId })}/>} 
    <button aria-label="View actions" className={styles.more} onClick={event => { event.preventDefault(); event.stopPropagation(); const rect = event.currentTarget.getBoundingClientRect(); event.currentTarget.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, clientX: event.clientX || rect.right, clientY: event.clientY || rect.bottom })) }} type="button"><Ellipsis size={15}/></button>
  </a>
  return <ContextMenu.Root><ContextMenu.Trigger asChild>{row}</ContextMenu.Trigger><ContextMenu.Portal><ContextMenu.Content className={styles.contextMenu} onKeyDown={event => { if (event.altKey && event.key.toLowerCase() === 'f') { event.preventDefault(); onToggleFavorite() } }}>
    <ViewMenuItem icon={<Pencil/>} label="Edit…" onSelect={onEdit}/>
    <ViewMenuItem icon={<Copy/>} label="Duplicate…" onSelect={onDuplicate}/>
    <ContextMenu.Sub><ContextMenu.SubTrigger className={styles.menuItem}><UserRound/><span>Owner</span><ChevronRight className={styles.chevron}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className={styles.contextMenu} sideOffset={-3}>{data.users.map(user => <ContextMenu.Item className={styles.menuItem} key={user.id} onSelect={() => onUpdate({ ownerId: user.id })}><ViewOwnerAvatar user={user}/><span data-i18n-ignore>{user.displayName}</span>{owner.id === user.id && <Check className={styles.check}/>}</ContextMenu.Item>)}</ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>
    <ContextMenu.Sub><ContextMenu.SubTrigger className={styles.menuItem}><Layers3/><span>Move to</span><ChevronRight className={styles.chevron}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className={styles.contextMenu} sideOffset={-3}><ViewMenuItem icon={<LockKeyhole/>} label="Personal" onSelect={() => onMove({ scope: 'personal' })}/><ViewMenuItem icon={<Building2/>} label="Workspace" onSelect={() => onMove({ scope: 'workspace' })}/><ContextMenu.Separator className={styles.separator}/>{data.teams.map(team => <ContextMenu.Item className={styles.menuItem} key={team.id} onSelect={() => onMove({ scope: 'team', teamId: team.id })}><TeamIcon/><span data-i18n-ignore>{team.name}</span></ContextMenu.Item>)}</ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>
    <ContextMenu.Separator className={styles.separator}/>
    <ViewMenuItem icon={<Star fill={favorite ? 'currentColor' : 'none'}/>} label={favorite ? 'Unfavorite' : 'Favorite'} onSelect={onToggleFavorite} shortcut="⌥ F"/>
    <ContextMenu.Sub><ContextMenu.SubTrigger className={styles.menuItem}>{subscribed ? <BellOff/> : <Bell/>}<span>Subscribe</span><ChevronRight className={styles.chevron}/></ContextMenu.SubTrigger><ContextMenu.Portal><ContextMenu.SubContent className={`${styles.contextMenu} ${styles.subscriptionMenu}`} sideOffset={-3}>
      <SubscriptionEventItem checked={subscriptionEvents.has('issue-added')} label={`${resource === 'issues' ? 'An issue' : 'A project'} is added to the view`} onSelect={() => toggleSubscriptionEvent('issue-added')}/>
      <SubscriptionEventItem checked={subscriptionEvents.has('issue-completed')} label={`${resource === 'issues' ? 'An issue is' : 'A project is'} marked completed or canceled`} onSelect={() => toggleSubscriptionEvent('issue-completed')}/>
    </ContextMenu.SubContent></ContextMenu.Portal></ContextMenu.Sub>
    <ViewMenuItem icon={<Copy/>} label="Copy link" onSelect={onCopy}/>
    <ContextMenu.Separator className={styles.separator}/>
    <ViewMenuItem danger icon={<Trash2/>} label="Delete" onSelect={onDelete}/>
  </ContextMenu.Content></ContextMenu.Portal></ContextMenu.Root>
}

function ViewsDisplayMenu({ direction, ordering, properties, onDirection, onOrdering, onToggleProperty }: { direction: Direction; ordering: Ordering; properties: Set<DisplayProperty>; onDirection: () => void; onOrdering: (ordering: Ordering) => void; onToggleProperty: (property: DisplayProperty) => void }) {
  return <Popover.Root>
    <Popover.Trigger asChild><button aria-label="Display options" className={styles.displayTrigger} type="button"><DisplayOptionsIcon/></button></Popover.Trigger>
    <Popover.Portal><Popover.Content align="end" className={styles.displayPopover} collisionPadding={11} onCloseAutoFocus={event => event.preventDefault()} onOpenAutoFocus={event => event.preventDefault()} sideOffset={4}>
      <div className={styles.displayOrderingSection}>
        <div className={styles.displayOrderingRow}>
          <span className={styles.displayLabel}>Ordering</span>
          <div className={styles.displayOrderingControls}>
            <button aria-label="Direction" className={styles.directionButton} onClick={onDirection} type="button"><OrderingDirectionIcon direction={direction}/></button>
            <Select.Root onValueChange={value => onOrdering(value as Ordering)} value={ordering}>
              <Select.Trigger aria-label="View ordering" className={styles.orderingSelect}><Select.Value/><Select.Icon><CompactChevron/></Select.Icon></Select.Trigger>
              <Select.Portal><Select.Content align="end" className={styles.orderingList} collisionPadding={11} position="popper" sideOffset={5}>
                <Select.Viewport>{(['name', 'owner', 'updated', 'created'] as const).map(item => <Select.Item className={styles.orderingOption} key={item} value={item}><Select.ItemText>{orderingLabel(item)}</Select.ItemText><Select.ItemIndicator className={styles.orderingIndicator}><FlowCheckIcon/></Select.ItemIndicator></Select.Item>)}</Select.Viewport>
              </Select.Content></Select.Portal>
            </Select.Root>
          </div>
        </div>
      </div>
      <div className={styles.displayPropertiesSection}>
        <span className={styles.displayPropertiesLabel}>Display properties</span>
        <div className={styles.displayPropertyOptions}>{(['created', 'updated', 'owner'] as const).map(property => <button aria-pressed={properties.has(property)} className={styles.displayProperty} data-active={properties.has(property) || undefined} key={property} onClick={() => onToggleProperty(property)} type="button">{orderingLabel(property)}</button>)}</div>
      </div>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

function OwnerMenu({ owner, users, onChange }: { owner: User; users: User[]; onChange: (ownerId: string) => void }) {
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={`${owner.displayName} ${owner.displayName}`} className={styles.owner} onClick={event => { event.preventDefault(); event.stopPropagation() }} type="button"><ViewOwnerAvatar user={owner}/><span data-i18n-ignore>{owner.displayName}</span></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className={styles.contextMenu} sideOffset={4}>{users.map(user => <DropdownMenu.Item className={styles.menuItem} key={user.id} onSelect={() => onChange(user.id)}><ViewOwnerAvatar user={user}/><span data-i18n-ignore>{user.displayName}</span>{user.id === owner.id && <Check className={styles.check}/>}</DropdownMenu.Item>)}</DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function ViewMenuItem({ danger = false, icon, label, onSelect, shortcut }: { danger?: boolean; icon: ReactNode; label: string; onSelect: () => void; shortcut?: string }) { return <ContextMenu.Item className={styles.menuItem} data-danger={danger || undefined} onSelect={onSelect}>{icon}<span>{label}</span>{shortcut && <kbd>{shortcut}</kbd>}</ContextMenu.Item> }
function SubscriptionEventItem({ checked, label, onSelect }: { checked: boolean; label: string; onSelect: () => void }) { return <ContextMenu.CheckboxItem checked={checked} className={styles.menuItem} onSelect={event => { event.preventDefault(); onSelect() }}><span className={styles.menuCheckbox}>{checked && <CheckboxMark/>}</span><span>{label}</span></ContextMenu.CheckboxItem> }
function SortButton({ active, direction, label, onClick }: { active: boolean; direction: Direction; label: string; onClick: () => void }) { return <button className={styles.sortButton} onClick={onClick} type="button"><span>{label}</span>{active && (direction === 'asc' ? <ArrowDown size={12}/> : <ArrowUp size={12}/>)}</button> }
function ScopeAvatar({ kind, label }: { kind: SavedView['scope']; label: string }) { return <span className={styles.scopeAvatar}>{kind === 'personal' ? <UserRound size={13}/> : kind === 'team' ? <TeamIcon size={13}/> : label.slice(0, 2).toUpperCase()}</span> }
function ViewOwnerAvatar({ user }: { user: User }) { return <UserAvatar avatarUrl={user.avatarUrl} className={styles.avatar} name={user.displayName}/> }
function FlowPlusIcon() { return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path d="M8.75 4C8.75 3.58579 8.41421 3.25 8 3.25C7.58579 3.25 7.25 3.58579 7.25 4V7.25H4C3.58579 7.25 3.25 7.58579 3.25 8C3.25 8.41421 3.58579 8.75 4 8.75H7.25V12C7.25 12.4142 7.58579 12.75 8 12.75C8.41421 12.75 8.75 12.4142 8.75 12V8.75H12C12.4142 8.75 12.75 8.41421 12.75 8C12.75 7.58579 12.4142 7.25 12 7.25H8.75V4Z"/></svg> }
function ViewFavoriteIcon({ selected }: { selected: boolean }) { return selected ? <svg aria-hidden="true" fill="lch(80% 90 85)" viewBox="0 0 16 16"><path d="M14.9441 6.05256C14.8798 5.88159 14.7646 5.73411 14.6136 5.6298C14.4626 5.52549 14.2832 5.46931 14.0992 5.46877H10.4417C10.3795 5.46881 10.3187 5.44969 10.2679 5.41405C10.2171 5.37841 10.1787 5.32801 10.1581 5.2698L8.84514 1.58061C8.78083 1.4101 8.66552 1.26313 8.51465 1.15936C8.36377 1.0556 8.18453 1 8.00091 1C7.81728 1 7.63804 1.0556 7.48717 1.15936C7.33629 1.26313 7.22098 1.4101 7.15667 1.58061L7.15367 1.59014L5.84375 5.2698C5.82313 5.32791 5.78483 5.37825 5.73415 5.41388C5.68346 5.44951 5.62288 5.46869 5.56075 5.46877H1.902C1.71682 5.46863 1.53608 5.52504 1.38439 5.63033C1.23269 5.73562 1.11739 5.88468 1.05416 6.05724C.990937 6.22979.982856 6.41746 1.03102 6.59473C1.07919 6.772 1.18126 6.93025 1.32335 7.04798L4.4383 9.6095C4.4849 9.64784 4.51872 9.69923 4.53534 9.75695C4.55196 9.81467 4.5506 9.87603 4.53143 9.93297L3.22272 13.8235C3.16224 14.0034 3.16105 14.1977 3.21932 14.3783C3.27758 14.5589 3.39229 14.7164 3.54684 14.8281C3.70139 14.9398 3.88777 15 4.07903 14.9996C4.27029 14.9994 4.45652 14.9388 4.61076 14.8267L7.82305 12.4915C7.87456 12.4541 7.93675 12.4339 8.00061 12.4339C8.06446 12.4339 8.12665 12.4541 8.17817 12.4915L11.3893 14.8261C11.5434 14.9386 11.7297 14.9995 11.9212 15C12.1126 15.0005 12.2992 14.9406 12.454 14.8289C12.6087 14.7172 12.7236 14.5595 12.782 14.3788C12.8403 14.198 12.8391 14.0035 12.7785 13.8235L11.4698 9.93059C11.4506 9.87364 11.4493 9.81229 11.4659 9.75457C11.4825 9.69685 11.5163 9.64545 11.5629 9.60712L14.6839 7.04202C14.8242 6.9233 14.9243 6.76477 14.9708 6.58783C15.0174 6.41089 15.0081 6.22406 14.9441 6.05256Z"/></svg> : <svg aria-hidden="true" fill="lch(61.803% 1.2 272)" viewBox="0 0 16 16"><path d="M10.5193 4.98997L9.46118 2.01693C9.34483 1.70806 9.1452 1.45362 8.88451 1.27433C8.62466 1.09562 8.31641 1 8.00081 1C7.68521 1 7.37696 1.09562 7.11712 1.27433C6.85642 1.45362 6.65679 1.70806 6.54528 2.00374L5.48248 4.98997H2.55536C2.23765 4.98973 1.92683 5.08675 1.66556 5.26809C1.40342 5.45004 1.20379 5.70812 1.09414 6.00737C.984248 6.30728.970192 6.63372 1.05394 6.94194C1.13753 7.2496 1.31442 7.52386 1.56019 7.7275L4.08545 9.80411L3.02371 12.9604C2.91854 13.2733 2.91647 13.6112 3.01776 13.9252C3.11884 14.2385 3.3175 14.5113 3.58464 14.7044C3.85102 14.8969 4.17178 15.0003 4.50071 14.9996C4.82872 14.9993 5.14907 14.8951 5.41483 14.702L8.00053 12.8223L10.5851 14.7014C10.8496 14.8944 11.17 14.9991 11.4991 15C11.8281 15.0009 12.1491 14.8978 12.4157 14.7054C12.6831 14.5124 12.882 14.2394 12.9833 13.926C13.0848 13.6113 13.0827 13.2731 12.9773 12.9602L11.9156 9.80207L14.444 7.72408C14.695 7.51166 14.8686 7.23684 14.9493 6.92968C15.0168 6.67352 15.0167 6.40505 14.9504 6.15011L14.9022 5.99753C14.791 5.70157 14.5918 5.44667 14.3314 5.26673C14.0718 5.08736 13.7637 4.9909 13.4479 4.98998L10.5193 4.98997ZM13.4986 6.54821C13.4962 6.55733 13.491 6.56562 13.4832 6.57224L10.7049 8.85551C10.546 8.98629 10.4307 9.16168 10.3739 9.35896C10.3168 9.55714 10.3214 9.76807 10.3875 9.96371L11.5556 13.4385C11.5586 13.4474 11.5587 13.4565 11.5559 13.4652C11.553 13.4741 11.5467 13.4827 11.5378 13.4891C11.5281 13.4961 11.5159 13.5 11.503 13.5C11.4902 13.5 11.4779 13.496 11.4683 13.4889L8.60012 11.4036C8.42554 11.2769 8.21577 11.2088 8.00055 11.2088C7.78531 11.2088 7.5755 11.2769 7.40134 11.4034L4.53289 13.4886C4.52321 13.4957 4.511 13.4996 4.49835 13.4996C4.48523 13.4997 4.47312 13.4958 4.46329 13.4887C4.45442 13.4822 4.44826 13.4738 4.4453 13.4646C4.44255 13.4561 4.4426 13.4471 4.44547 13.4386L5.61393 9.96499C5.67961 9.76981 5.68428 9.5592 5.62728 9.3612C5.57043 9.16375 5.45499 8.98835 5.29643 8.85789L2.51507 6.57069C2.50925 6.56586 2.50387 6.55753 2.50146 6.54865C2.49919 6.54032 2.49957 6.53163 2.50257 6.52343C2.50583 6.51453 2.5121 6.50643 2.52085 6.50035C2.53046 6.49368 2.54238 6.48996 2.55479 6.48997H5.8221C6.03248 6.4897 6.23685 6.42501 6.40824 6.30453C6.58053 6.18341 6.71109 6.01179 6.78158 5.81318L7.9609 2.49821C7.95727 2.50944 7.95646 2.51419 7.9574 2.5155C7.97668 2.50367 7.98851 2.5 8.00081 2.5C8.01311 2.5 8.02494 2.50367 8.03451 2.51025C8.04324 2.51625 8.04952 2.52427 8.05284 2.53307L9.22029 5.81379C9.29053 6.01192 9.42137 6.18383 9.59407 6.30503C9.76589 6.4256 9.97082 6.49011 10.1806 6.48997H13.4457C13.4563 6.49001 13.4686 6.49385 13.4786 6.50077L13.4902 6.5114L13.4977 6.52418C13.5004 6.53198 13.5007 6.54022 13.4986 6.54821Z"/></svg> }
function DisplayOptionsIcon() { return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path clipRule="evenodd" d="M7 2.5C8.11933 2.5 9.06613 3.23584 9.38477 4.25H14.75C15.1642 4.25 15.5 4.58579 15.5 5C15.5 5.41421 15.1642 5.75 14.75 5.75H9.38477C9.06613 6.76416 8.11933 7.5 7 7.5C5.88067 7.5 4.93387 6.76416 4.61523 5.75H2.25C1.83579 5.75 1.5 5.41421 1.5 5C1.5 4.58579 1.83579 4.25 2.25 4.25H4.61523C4.93387 3.23584 5.88067 2.5 7 2.5ZM7 4C6.44772 4 6 4.44772 6 5C6 5.55228 6.44772 6 7 6C7.55228 6 8 5.55228 8 5C8 4.44772 7.55228 4 7 4Z" fillRule="evenodd"/><path clipRule="evenodd" d="M10 13.5C8.88067 13.5 7.93387 12.7642 7.61523 11.75H2.25C1.83579 11.75 1.5 11.4142 1.5 11C1.5 10.5858 1.83579 10.25 2.25 10.25H7.61523C7.93387 9.23584 8.88067 8.5 10 8.5C11.1193 8.5 12.0661 9.23584 12.3848 10.25H14.75C15.1642 10.25 15.5 10.5858 15.5 11C15.5 11.4142 15.1642 11.75 14.75 11.75H12.3848C12.0661 12.7642 11.1193 13.5 10 13.5ZM10 12C10.5523 12 11 11.5523 11 11C11 10.4477 10.5523 10 10 10C9.44772 10 9 10.4477 9 11C9 11.5523 9.44772 12 10 12Z" fillRule="evenodd"/></svg> }
function OrderingDirectionIcon({ direction }: { direction: Direction }) { return direction === 'asc' ? <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path clipRule="evenodd" d="M8.23 12.326a.75.75 0 1 0-.96-1.152L5.5 12.649V1.75a.75.75 0 0 0-1.5 0v10.899l-1.77-1.475a.75.75 0 1 0-.96 1.152l3 2.5a.75.75 0 0 0 .96 0z" fillRule="evenodd"/><path clipRule="evenodd" d="M7 8.75c0 .414.336.75.75.75h6.5a.75.75 0 0 0 0-1.5h-6.5a.75.75 0 0 0-.75.75M7 5.75c0 .414.336.75.75.75h4.5a.75.75 0 0 0 0-1.5h-4.5a.75.75 0 0 0-.75.75M7 2.75c0 .414.336.75.75.75h1.5a.75.75 0 1 0 0-1.5h-1.5a.75.75 0 0 0-.75.75" fillRule="evenodd"/></svg> : <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path clipRule="evenodd" d="M1.27 3.674a.75.75 0 1 0 .96 1.152L4 3.351V14.25a.75.75 0 0 0 1.5 0V3.351l1.77 1.475a.75.75 0 1 0 .96-1.152l-3-2.5a.75.75 0 0 0-.96 0z" fillRule="evenodd"/><path clipRule="evenodd" d="M7 7.25A.75.75 0 0 1 7.75 6.5h6.5a.75.75 0 0 1 0 1.5h-6.5A.75.75 0 0 1 7 7.25M7 10.25a.75.75 0 0 1 .75-.75h4.5a.75.75 0 0 1 0 1.5h-4.5A.75.75 0 0 1 7 10.25M7 13.25a.75.75 0 0 1 .75-.75h1.5a.75.75 0 1 1 0 1.5h-1.5A.75.75 0 0 1 7 13.25" fillRule="evenodd"/></svg> }
function CompactChevron() { return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 9 5"><path d="M1.915.557a.667.667 0 0 0-.943.943l2.862 2.862a.942.942 0 0 0 1.333 0L8.028 1.5a.667.667 0 0 0-.943-.943L4.5 3.14 1.915.557Z"/></svg> }
function FlowCheckIcon() { return <svg aria-hidden="true" fill="currentColor" viewBox="0 0 16 16"><path d="M6.336 13.6a1.049 1.049 0 0 1-.8-.376L2.632 9.736a.992.992 0 0 1 .152-1.424 1.056 1.056 0 0 1 1.456.152l2.008 2.4 5.448-8a1.048 1.048 0 0 1 1.432-.288A.992.992 0 0 1 13.424 4L7.2 13.144a1.04 1.04 0 0 1-.8.456h-.064Z"/></svg> }
function orderingLabel(value: Ordering | DisplayProperty) { return value[0].toUpperCase() + value.slice(1) }
function formatDate(value: string) { return new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' }).format(new Date(value)) }
function viewDescription(view: SavedView, resource: ViewsResource) { if (view.description) return view.description; const count = Array.isArray(view.filters) ? view.filters.length : 0; return count ? `${resource === 'issues' ? 'Issues' : 'Projects'} matching ${count} ${count === 1 ? 'filter' : 'filters'}` : `All ${resource}` }
function columnTemplate(properties: Set<DisplayProperty>) { return `minmax(240px, 1fr)${properties.has('created') ? ' 110px' : ''}${properties.has('updated') ? ' 110px' : ''}${properties.has('owner') ? ' 120px' : ''}` }
function readProperties(key: string): DisplayProperty[] { try { const value = JSON.parse(localStorage.getItem(key) ?? '["owner"]'); return Array.isArray(value) ? value.filter(item => ['created', 'owner', 'updated'].includes(item)) : ['owner'] } catch { return ['owner'] } }
function readPreference(key: string, fallback: string) { try { return localStorage.getItem(key) ?? fallback } catch { return fallback } }
function writePreference(key: string, value: string) { try { localStorage.setItem(key, value) } catch { /* Display preferences are best-effort. */ } }
function compareViews(left: SavedView, right: SavedView, ordering: Ordering, direction: Direction, users: Map<string, User>, viewer: User) { const factor = direction === 'asc' ? 1 : -1; const leftValue = ordering === 'name' ? left.name : ordering === 'owner' ? (users.get(left.ownerId ?? '') ?? viewer).displayName : ordering === 'created' ? left.createdAt : left.updatedAt; const rightValue = ordering === 'name' ? right.name : ordering === 'owner' ? (users.get(right.ownerId ?? '') ?? viewer).displayName : ordering === 'created' ? right.createdAt : right.updatedAt; return leftValue.localeCompare(rightValue) * factor }
