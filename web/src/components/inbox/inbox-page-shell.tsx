import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Select from '@radix-ui/react-select'
import type { ButtonHTMLAttributes, CSSProperties, KeyboardEvent as ReactKeyboardEvent, PointerEvent, ReactNode } from 'react'
import { forwardRef, useEffect, useRef, useState } from 'react'

import { InboxFilterBuilder, type InboxFilterCondition, type InboxFilterOptions } from './inbox-filter-builder'
import './inbox.css'
import { PhoneMenu } from '@/components/ui/phone-menu'
import { CheckIcon, DisplayIcon, FilterIcon, SidebarIcon } from '@/components/ui/view-action-icons'
import { FlowTooltip } from '@/components/ui/tooltip'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import {
  SPLIT_VIEW_DEFAULT_LIST_WIDTH,
  SPLIT_VIEW_MIN_DETAIL_WIDTH,
  SPLIT_VIEW_MIN_LIST_WIDTH,
  persistSplitViewListWidth,
  readSplitViewListWidth,
} from '@/components/split-view'

export type InboxOrdering = 'newest' | 'oldest' | 'priority'
export type InboxUnreadGrouping = 'none' | 'focus'
export type InboxTab = 'all' | 'priority' | 'other'

export interface InboxDisplayOptions {
  ordering: InboxOrdering
  showSnoozed: boolean
  showRead: boolean
  showUnreadFirst: boolean
  priorityInbox: boolean
  unreadGrouping: InboxUnreadGrouping
}

export interface InboxPageShellProps {
  children: ReactNode
  detail?: ReactNode
  filters?: InboxFilterCondition[]
  filterOptions?: InboxFilterOptions
  filterHiddenCount?: number
  showFilterFooter?: boolean
  bulkPending?: boolean
  onFiltersChange?: (filters: InboxFilterCondition[]) => void
  displayOptions: InboxDisplayOptions
  onDisplayOptionsChange: (options: InboxDisplayOptions) => void
  onDeleteAll: () => void
  onDeleteAllRead: () => void
  onDeleteAllReadCompleted: () => void
  onMarkAllRead?: () => void
  onOpenSettings?: () => void
  onOpenSidebar?: () => void
  activeTab?: InboxTab
  onTabChange?: (tab: InboxTab) => void
  tabCounts?: Partial<Record<InboxTab, number>>
}

const DEFAULT_LIST_WIDTH = SPLIT_VIEW_DEFAULT_LIST_WIDTH.inbox
const MIN_LIST_WIDTH = SPLIT_VIEW_MIN_LIST_WIDTH
const MIN_DETAIL_WIDTH = SPLIT_VIEW_MIN_DETAIL_WIDTH

export function InboxPageShell({
  children,
  detail,
  filters,
  filterOptions,
  filterHiddenCount = 0,
  showFilterFooter = true,
  bulkPending = false,
  onFiltersChange,
  displayOptions,
  onDisplayOptionsChange,
  onDeleteAll,
  onDeleteAllRead,
  onDeleteAllReadCompleted,
  onMarkAllRead,
  onOpenSettings,
  onOpenSidebar,
  activeTab = 'all',
  onTabChange,
  tabCounts,
}: InboxPageShellProps) {
  const shellRef = useRef<HTMLElement>(null)
  const dragRef = useRef({ active: false, pointerId: -1 })
  const [preferredListWidth, setPreferredListWidth] = useState(readInboxListWidth)
  const [shellWidth, setShellWidth] = useState(0)
  const [resizing, setResizing] = useState(false)
  const maximumListWidth = Math.max(MIN_LIST_WIDTH, shellWidth - MIN_DETAIL_WIDTH)
  const listWidth = clamp(preferredListWidth, MIN_LIST_WIDTH, maximumListWidth)

  useEffect(() => {
    const shell = shellRef.current
    if (!shell) return
    const observer = new ResizeObserver(([entry]) => setShellWidth(entry.contentRect.width))
    observer.observe(shell)
    setShellWidth(shell.getBoundingClientRect().width)
    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    if (!resizing) return
    document.documentElement.classList.add('flow-inbox-is-resizing')
    return () => document.documentElement.classList.remove('flow-inbox-is-resizing')
  }, [resizing])

  const updateListWidth = (width: number) => {
    const nextWidth = clamp(width, MIN_LIST_WIDTH, maximumListWidth)
    setPreferredListWidth(nextWidth)
    persistInboxListWidth(nextWidth)
  }
  const startResize = (event: PointerEvent<HTMLButtonElement>) => {
    dragRef.current = { active: true, pointerId: event.pointerId }
    event.currentTarget.setPointerCapture(event.pointerId)
    setResizing(true)
    event.preventDefault()
  }
  const resize = (event: PointerEvent<HTMLButtonElement>) => {
    if (!dragRef.current.active || dragRef.current.pointerId !== event.pointerId) return
    const shell = shellRef.current
    if (!shell) return
    updateListWidth(event.clientX - shell.getBoundingClientRect().left)
  }
  const stopResize = (event: PointerEvent<HTMLButtonElement>) => {
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId)
    dragRef.current = { active: false, pointerId: -1 }
    setResizing(false)
  }
  const resizeWithKeyboard = (event: ReactKeyboardEvent<HTMLButtonElement>) => {
    const step = event.shiftKey ? 50 : 10
    const nextWidth = event.key === 'ArrowLeft'
      ? listWidth - step
      : event.key === 'ArrowRight'
        ? listWidth + step
        : event.key === 'Home'
          ? MIN_LIST_WIDTH
          : event.key === 'End'
            ? maximumListWidth
            : undefined
    if (nextWidth === undefined) return
    event.preventDefault()
    updateListWidth(nextWidth)
  }

  return (
    <main
      ref={shellRef}
      className="flow-inbox"
      aria-label="Inbox"
      data-detail-open={detail !== undefined}
      data-resizing={resizing || undefined}
      style={{ '--inbox-list-width': `${listWidth}px` } as CSSProperties}
    >
      <section className="flow-inbox__list-pane">
        <InboxHeader
          filters={filters}
          filterOptions={filterOptions}
          bulkPending={bulkPending}
          onFiltersChange={onFiltersChange}
          displayOptions={displayOptions}
          onDisplayOptionsChange={onDisplayOptionsChange}
          onDeleteAll={onDeleteAll}
          onDeleteAllRead={onDeleteAllRead}
          onDeleteAllReadCompleted={onDeleteAllReadCompleted}
          onMarkAllRead={onMarkAllRead}
          onOpenSettings={onOpenSettings}
          onOpenSidebar={onOpenSidebar}
          activeTab={activeTab}
          onTabChange={onTabChange}
          tabCounts={tabCounts}
        />
        <div className="flow-inbox__list">
          {children}
        </div>
        {filterHiddenCount > 0 && showFilterFooter ? <div className="flow-inbox__filter-footer" role="status">
          <span><strong>{filterHiddenCount} {filterHiddenCount === 1 ? 'notification' : 'notifications'}</strong> hidden by filters</span>
          <button type="button" onClick={() => onFiltersChange?.([])}>Clear Filters</button>
        </div> : null}
      </section>
      <button
        className="flow-inbox__resize-handle"
        type="button"
        role="separator"
        aria-label="Resize Inbox list"
        aria-orientation="vertical"
        aria-valuemin={MIN_LIST_WIDTH}
        aria-valuemax={Math.round(maximumListWidth)}
        aria-valuenow={Math.round(listWidth)}
        onKeyDown={resizeWithKeyboard}
        onPointerDown={startResize}
        onPointerMove={resize}
        onPointerUp={stopResize}
        onPointerCancel={stopResize}
      />
      <section className="flow-inbox__detail" aria-label="Notification preview">
        {detail ?? <InboxNoSelection />}
      </section>
    </main>
  )
}

function readInboxListWidth() {
  return readSplitViewListWidth('inbox', DEFAULT_LIST_WIDTH)
}

function persistInboxListWidth(width: number) {
  persistSplitViewListWidth('inbox', width)
}

function clamp(value: number, minimum: number, maximum: number) {
  return Math.min(maximum, Math.max(minimum, value))
}

interface InboxHeaderProps extends Omit<InboxPageShellProps, 'children' | 'detail'> {}

export function InboxHeader({
  filters,
  filterOptions,
  bulkPending = false,
  onFiltersChange,
  displayOptions,
  onDisplayOptionsChange,
  onDeleteAll,
  onDeleteAllRead,
  onDeleteAllReadCompleted,
  onMarkAllRead,
  onOpenSettings,
  onOpenSidebar,
}: InboxHeaderProps) {
  return (
    <header className="flow-inbox__header">
      {onOpenSidebar ? (
        <button
          className="flow-inbox__icon-button flow-inbox__mobile-menu"
          type="button"
          aria-label="Open sidebar"
          data-sidebar-trigger onClick={onOpenSidebar}
        >
          <SidebarIcon />
        </button>
      ) : null}
      <div className="flow-inbox__header-title-actions">
        <h2>Inbox</h2>
        <NotificationActionsMenu
          pending={bulkPending}
          onMarkAllRead={onMarkAllRead}
          onDeleteAll={onDeleteAll}
          onDeleteAllRead={onDeleteAllRead}
          onDeleteAllReadCompleted={onDeleteAllReadCompleted}
          onOpenSettings={onOpenSettings}
        />
      </div>
      <div className="flow-inbox__header-spacer" />
      <div className="flow-inbox__header-toolbar-group">
        <div className="flow-inbox__header-controls">
          <FlowTooltip label="Show unreads only">
            <button
              aria-label="Show unreads only"
              aria-pressed={!displayOptions.showRead}
              className="flow-inbox__icon-button"
              data-active={!displayOptions.showRead || undefined}
              onClick={() => onDisplayOptionsChange({ ...displayOptions, showRead: !displayOptions.showRead })}
              type="button"
            ><UnreadFilterIcon /></button>
          </FlowTooltip>
          <InboxFilterBuilder
            trigger={<IconButton label="Add filter" tooltip="Add Filter" shortcut="F" count={filters?.length ?? 0}><FilterIcon /></IconButton>}
            filters={filters ?? []}
            options={filterOptions}
            onFiltersChange={nextFilters => onFiltersChange?.(nextFilters)}
          />
          <DisplayOptionsMenu value={displayOptions} onChange={onDisplayOptionsChange} />
        </div>
      </div>
    </header>
  )
}

function UnreadFilterIcon() {
  return <svg aria-hidden="true" viewBox="0 0 16 16"><path fill="currentColor" fillRule="evenodd" d="M7.25 1C7.66421 1 8 1.33579 8 1.75C8 2.16421 7.66421 2.5 7.25 2.5H5.17969C4.79573 2.50003 4.45093 2.71902 4.28418 3.05469L4.22363 3.20605L2.74902 8H4.37305C5.32773 8 6.21946 8.47714 6.74902 9.27148L6.78809 9.32227C6.8848 9.43414 7.02582 9.49992 7.17578 9.5H8.82422C8.99571 9.49991 9.15584 9.41418 9.25098 9.27148L9.35449 9.12598C9.89229 8.41918 10.732 8 11.627 8H14.8115C14.8169 8.0002 14.8215 8.00369 14.8232 8.00879C14.9404 8.38972 15 8.78603 15 9.18457V11.5L14.9951 11.6797C14.9016 13.5292 13.3727 15 11.5 15H4.5C2.62727 15 1.09842 13.5292 1.00488 11.6797L1 11.5V9.18457C1.00002 8.88558 1.03338 8.5878 1.09961 8.29688L1.17676 8.00879L2.79004 2.76465C3.09264 1.78125 3.96362 1.09029 4.97559 1.00781L5.17969 1H7.25ZM2.5 11.5C2.5 12.6046 3.39543 13.5 4.5 13.5H11.5C12.6046 13.5 13.5 12.6046 13.5 11.5V9.5H11.627C11.2304 9.5 10.8572 9.6738 10.6016 9.96973L10.499 10.1035C10.1257 10.6635 9.49724 10.9999 8.82422 11H7.17578C6.54494 10.9999 5.95335 10.7043 5.57422 10.2061L5.50098 10.1035C5.24961 9.72647 4.8262 9.5 4.37305 9.5H2.5V11.5Z" /><path fill="currentColor" d="M12.5 1C13.8807 1 15 2.11929 15 3.5C15 4.88071 13.8807 6 12.5 6C11.1193 6 10 4.88071 10 3.5C10 2.11929 11.1193 1 12.5 1Z" /></svg>
}

function NotificationActionsMenu({
  pending = false,
  onMarkAllRead,
  onDeleteAll,
  onDeleteAllRead,
  onDeleteAllReadCompleted,
  onOpenSettings,
}: Pick<
  InboxPageShellProps,
  'onMarkAllRead' | 'onDeleteAll' | 'onDeleteAllRead' | 'onDeleteAllReadCompleted' | 'onOpenSettings'
> & { pending?: boolean }) {
  const phone = usePhoneLayout()
  const [phoneOpen, setPhoneOpen] = useState(false)
  useInboxShortcut('Backspace', event => {
    if (!event.shiftKey) return false
    onDeleteAllRead()
    return true
  })
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.key.toLowerCase() !== 'u' || !event.altKey || event.metaKey || event.ctrlKey || isEditableTarget(event.target)) return
      if (!onMarkAllRead) return
      event.preventDefault()
      onMarkAllRead()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onMarkAllRead])

  if (phone) {
    return (
      <>
        <IconButton label="Notification actions" aria-expanded={phoneOpen} aria-haspopup="dialog" onClick={() => setPhoneOpen(true)}>
          <MoreIcon />
        </IconButton>
        <InboxPhoneActionsMenu
          open={phoneOpen}
          onOpenChange={setPhoneOpen}
          pending={pending}
          onDeleteAll={onDeleteAll}
          onDeleteAllRead={onDeleteAllRead}
          onDeleteAllReadCompleted={onDeleteAllReadCompleted}
          onOpenSettings={onOpenSettings}
        />
      </>
    )
  }

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <IconButton label="Notification actions">
          <MoreIcon />
        </IconButton>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content data-flow-motion="floating"
          className="flow-inbox-menu flow-inbox-menu--actions"
          side="bottom"
          align="start"
          sideOffset={4}
        >
          <InboxMenuItem disabled={pending || !onMarkAllRead} icon={<MarkAllReadIcon />} shortcut="⌥U" onSelect={() => onMarkAllRead?.()}>
            Mark all as read
          </InboxMenuItem>
          <DropdownMenu.Separator className="flow-inbox-menu__separator" />
          <InboxMenuItem disabled={pending} icon={<DeleteInboxIcon />} onSelect={onDeleteAll}>
            Delete all
          </InboxMenuItem>
          <DropdownMenu.Separator className="flow-inbox-menu__separator" />
          <InboxMenuItem disabled={pending || !onOpenSettings} icon={<SettingsInboxIcon />} onSelect={() => onOpenSettings?.()}>
            Go to settings
          </InboxMenuItem>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

function InboxPhoneActionsMenu({
  open,
  onOpenChange,
  pending = false,
  onDeleteAll,
  onDeleteAllRead,
  onDeleteAllReadCompleted,
  onOpenSettings,
}: Pick<
  InboxPageShellProps,
  'onDeleteAll' | 'onDeleteAllRead' | 'onDeleteAllReadCompleted' | 'onOpenSettings'
> & { open: boolean; onOpenChange: (open: boolean) => void; pending?: boolean }) {
  const [query, setQuery] = useState('')
  const searching = query.trim().toLowerCase()
  const actions = [
    { id: 'delete-all', label: 'Delete all', onSelect: onDeleteAll },
    { id: 'delete-all-read', label: 'Delete all read', onSelect: onDeleteAllRead },
    { id: 'delete-all-completed', label: 'Delete all completed', onSelect: onDeleteAllReadCompleted },
  ].filter(action => action.label.toLowerCase().includes(searching))
  const showSettings = 'go to settings'.includes(searching)
  const settingsTop = 6 + actions.length * 46 + (actions.length ? 12 : 0)
  const listHeight = 6 + actions.length * 46 + (actions.length && showSettings ? 12 : 0) + (showSettings ? 46 : 0) + 6 - 2
  return (
    <PhoneMenu
      open={open}
      onOpenChange={onOpenChange}
      label="Notification actions"
      className="inbox-actions-phone-menu"
    >
      <div className="phone-menu-search">
        <input
          type="text"
          aria-label="Command menu"
          autoFocus
          placeholder="Filter…"
          value={query}
          onChange={event => setQuery(event.target.value)}
        />
      </div>
      <div className="inbox-actions-phone-menu__list" style={{ height: listHeight }}>
        {actions.map((action, index) => (
          <button
            key={action.id}
            type="button"
            role="menuitem"
            data-testid={`inbox-actions-${action.id}`}
            className="inbox-actions-phone-menu__row"
            style={{ top: 6 + index * 46 }}
            disabled={pending}
            onClick={() => { action.onSelect(); onOpenChange(false) }}
          >
            <span className="inbox-actions-phone-menu__icon"><PhoneDeleteInboxIcon /></span>
            <span className="inbox-actions-phone-menu__label">{action.label}</span>
          </button>
        ))}
        {showSettings ? (
          <>
            {actions.length ? <div className="inbox-actions-phone-menu__separator" role="separator" data-testid="inbox-actions-divider" style={{ top: 6 + actions.length * 46 }} /> : null}
            <button
              className="inbox-actions-phone-menu__row"
              data-testid="inbox-actions-settings"
              type="button"
              role="menuitem"
              style={{ top: settingsTop }}
              onClick={() => { onOpenSettings?.(); onOpenChange(false) }}
            >
              <span className="inbox-actions-phone-menu__icon"><PhoneSettingsInboxIcon /></span>
              <span className="inbox-actions-phone-menu__label">Go to settings</span>
            </button>
          </>
        ) : null}
      </div>
    </PhoneMenu>
  )
}

function DisplayOptionsMenu({
  value,
  onChange,
}: {
  value: InboxDisplayOptions
  onChange: (value: InboxDisplayOptions) => void
}) {
  const update = (change: Partial<InboxDisplayOptions>) => onChange({ ...value, ...change })
  const [open, setOpen] = useState(false)
  useEffect(() => {
    // The reference app: ⇧V shows display options.
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || !event.shiftKey || event.altKey || event.metaKey || event.ctrlKey || event.key.toLowerCase() !== 'v' || isEditableTarget(event.target)) return
      event.preventDefault()
      setOpen(current => !current)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])
  return (
    <DropdownMenu.Root open={open} onOpenChange={setOpen}>
      <DropdownMenu.Trigger asChild>
        <IconButton label="Display options" tooltip={open ? undefined : 'Show display options'} shortcut="⇧ V">
          <DisplayIcon />
        </IconButton>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content data-flow-motion="floating"
          className="flow-inbox-menu flow-inbox-menu--display"
          side="bottom"
          align="end"
          sideOffset={4}
        >
          <div className="flow-inbox-menu__priority-row">
            <span>Enable priority inbox</span>
            <button
              type="button"
              role="checkbox"
              aria-checked={value.priorityInbox}
              aria-label="Enable priority inbox"
              className="flow-inbox-menu__native-switch"
              onClick={() => update({ priorityInbox: !value.priorityInbox })}
            />
          </div>
          <div className="flow-inbox-menu__separator" />
          <div className="flow-inbox-menu__display-row flow-inbox-menu__display-row--first">
            <span>Group unreads by</span>
            <UnreadGroupingSelect value={value.unreadGrouping} onChange={unreadGrouping => update({ unreadGrouping })} />
          </div>
          <div className="flow-inbox-menu__ordering">
            <span aria-label="View ordering">Ordering</span>
            <OrderingSelect value={value.ordering} onChange={ordering => update({ ordering })} />
          </div>
          <div className="flow-inbox-menu__separator" />
          <div className="flow-inbox-menu__toggle-options">
            <CheckboxItem
              checked={value.showSnoozed}
              onCheckedChange={(checked) => update({ showSnoozed: checked === true })}
            >
              Show snoozed
            </CheckboxItem>
            <CheckboxItem
              checked={value.showUnreadFirst}
              onCheckedChange={(checked) => update({ showUnreadFirst: checked === true })}
            >
              Show unread first
            </CheckboxItem>
          </div>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

function UnreadGroupingSelect({ value, onChange }: { value: InboxUnreadGrouping; onChange: (value: InboxUnreadGrouping) => void }) {
  const labels: Record<InboxUnreadGrouping, string> = { none: 'No grouping', focus: 'Focus' }
  return <Select.Root value={value} onValueChange={next => onChange(next as InboxUnreadGrouping)}>
    <Select.Trigger className="flow-inbox-grouping-select" aria-label="Change unread grouping">
      <Select.Value>{labels[value]}</Select.Value><Select.Icon><ChevronDownIcon /></Select.Icon>
    </Select.Trigger>
    <Select.Portal><Select.Content data-flow-motion="floating" className="flow-inbox-grouping-options" position="popper" sideOffset={5} align="end"><Select.Viewport>{(Object.keys(labels) as InboxUnreadGrouping[]).map(option => <Select.Item className="flow-inbox-ordering-option" value={option} key={option}><Select.ItemText>{labels[option]}</Select.ItemText><Select.ItemIndicator><CheckIcon /></Select.ItemIndicator></Select.Item>)}</Select.Viewport></Select.Content></Select.Portal>
  </Select.Root>
}

function OrderingSelect({ value, onChange }: { value: InboxOrdering; onChange: (value: InboxOrdering) => void }) {
  const labels: Record<InboxOrdering, string> = { newest: 'Newest', oldest: 'Oldest', priority: 'Priority' }
  return (
    <Select.Root value={value} onValueChange={nextValue => onChange(nextValue as InboxOrdering)}>
      <Select.Trigger className="flow-inbox-ordering-select">
        <Select.Value>{labels[value]}</Select.Value>
        <Select.Icon><ChevronDownIcon /></Select.Icon>
      </Select.Trigger>
    <Select.Portal>
        <Select.Content data-flow-motion="floating" className="flow-inbox-ordering-options" position="popper" sideOffset={5} align="end">
          <Select.Viewport>
            {(Object.keys(labels) as InboxOrdering[]).map(option => (
              <Select.Item className="flow-inbox-ordering-option" value={option} key={option}>
                <Select.ItemText>{labels[option]}</Select.ItemText>
                <Select.ItemIndicator><CheckIcon /></Select.ItemIndicator>
              </Select.Item>
            ))}
          </Select.Viewport>
        </Select.Content>
      </Select.Portal>
    </Select.Root>
  )
}

function InboxMenuItem({
  children,
  icon,
  shortcut,
  trailing,
  active = false,
  disabled = false,
  onSelect,
}: {
  children: ReactNode
  icon?: ReactNode
  shortcut?: string
  trailing?: ReactNode
  active?: boolean
  disabled?: boolean
  onSelect: () => void
}) {
  return (
    <DropdownMenu.Item className="flow-inbox-menu__item" data-active={active || undefined} disabled={disabled} onSelect={onSelect}>
      {icon ? <span className="flow-inbox-menu__item-icon">{icon}</span> : null}
      <span className="flow-inbox-menu__item-label">{children}</span>
      {shortcut ? <kbd className="flow-inbox-menu__shortcut" aria-label={shortcut}>{[...shortcut].map((key, index) => <span key={index}>{key}</span>)}</kbd> : null}
      {trailing ? <span className="flow-inbox-menu__trailing">{trailing}</span> : null}
    </DropdownMenu.Item>
  )
}

function CheckboxItem({
  children,
  checked,
  onCheckedChange,
}: {
  children: ReactNode
  checked: boolean
  onCheckedChange: (checked: boolean | 'indeterminate') => void
}) {
  return (
    <DropdownMenu.CheckboxItem
      className="flow-inbox-menu__check-item"
      checked={checked}
      onCheckedChange={onCheckedChange}
      onSelect={(event) => event.preventDefault()}
    >
      <span>{children}</span>
      <span className="flow-inbox-switch" aria-hidden="true">
        <i />
      </span>
    </DropdownMenu.CheckboxItem>
  )
}

function useInboxShortcut(key: string, action: (event: KeyboardEvent) => boolean | void) {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.key.toLowerCase() !== key.toLowerCase() || event.metaKey || event.ctrlKey || event.altKey || isEditableTarget(event.target)) return
      if (action(event)) event.preventDefault()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [action, key])
}

function isEditableTarget(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) return false
  // Keys typed inside an embedded review page belong to its own shortcuts.
  if (target.closest('[data-embedded-review]')) return true
  return target.isContentEditable || target.matches('input, textarea, select, [role="textbox"], [role="searchbox"]')
}

interface IconButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  label: string
  count?: number
  /** The reference app's hover tooltip (the ⋯ menu has none). */
  tooltip?: string
  shortcut?: string
  children: ReactNode
}

const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton({ label, count, tooltip, shortcut, children, ...buttonProps }, ref) {
  const button = (
    <button {...buttonProps} ref={ref} className="flow-inbox__icon-button" type="button" aria-label={label}>
      {children}
      {count ? <span className="flow-inbox__filter-count">{count}</span> : null}
    </button>
  )
  return tooltip ? <FlowTooltip label={tooltip} shortcut={shortcut}>{button}</FlowTooltip> : button
})

export function InboxNoSelection() {
  return (
    <div className="flow-inbox__no-selection">
      <InboxTrayIllustration />
      <span>No notification selected</span>
    </div>
  )
}

function MoreIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3 6.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Z" /></svg>
}


function MarkAllReadIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path fillRule="evenodd" clipRule="evenodd" d="M7.25 1a.75.75 0 0 1 0 1.5H5.18a1 1 0 0 0-.956.706L2.75 8h1.623c.955 0 1.846.477 2.376 1.272a.51.51 0 0 0 .427.228h1.648a.51.51 0 0 0 .427-.228A2.856 2.856 0 0 1 11.627 8H14.5l.323.009c.117.38.177.777.177 1.176V11.5a3.5 3.5 0 0 1-3.5 3.5h-7A3.5 3.5 0 0 1 1 11.5V9.185c0-.299.033-.597.1-.888l.077-.288L2.79 2.765A2.5 2.5 0 0 1 5.18 1h2.07ZM2.5 9.5v2a2 2 0 0 0 2 2h7a2 2 0 0 0 2-2v-2h-1.873c-.397 0-.77.174-1.025.47l-.103.134A2.014 2.014 0 0 1 8.824 11H7.176a2.014 2.014 0 0 1-1.675-.896l-.103-.134a1.356 1.356 0 0 0-1.025-.47H2.5Zm12.28-8.28a.75.75 0 0 1 0 1.06l-3.25 3.25a.75.75 0 0 1-1.06 0L8.97 4.03a.75.75 0 0 1 1.06-1.06l.97.97 2.72-2.72a.75.75 0 0 1 1.06 0Z" /></svg>
}

export function DeleteInboxIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path fillRule="evenodd" clipRule="evenodd" d="M7.25 1a.75.75 0 0 1 0 1.5H5.18a1 1 0 0 0-.956.706L2.75 8h1.623c.955 0 1.846.477 2.376 1.272a.51.51 0 0 0 .427.228h1.648a.51.51 0 0 0 .427-.228A2.856 2.856 0 0 1 11.627 8H14.5l.323.009c.117.38.177.777.177 1.176V11.5a3.5 3.5 0 0 1-3.5 3.5h-7A3.5 3.5 0 0 1 1 11.5V9.185c0-.299.033-.597.1-.888l.077-.288L2.79 2.765A2.5 2.5 0 0 1 5.18 1h2.07ZM2.5 9.5v2a2 2 0 0 0 2 2h7a2 2 0 0 0 2-2v-2h-1.873c-.397 0-.77.174-1.025.47l-.103.134A2.014 2.014 0 0 1 8.824 11H7.176a2.014 2.014 0 0 1-1.675-.896l-.103-.134a1.356 1.356 0 0 0-1.025-.47H2.5Zm11.22-8.28a.75.75 0 0 1 1.06 1.06L13.561 3.5l1.22 1.22a.75.75 0 1 1-1.061 1.06L12.5 4.561l-1.22 1.22a.75.75 0 1 1-1.06-1.061l1.219-1.22-1.22-1.22a.75.75 0 1 1 1.061-1.06l1.22 1.219 1.22-1.22Z" /></svg>
}

function PhoneDeleteInboxIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true" fill="currentColor"><path d="M7.25 1.00006C7.66421 1.00006 8 1.33585 8 1.75006C7.99997 2.16425 7.66419 2.50006 7.25 2.50006H5.17969C4.79573 2.50009 4.45093 2.71908 4.28418 3.05475L4.22363 3.20612L2.74902 8.00006H4.37305C5.32773 8.00006 6.21946 8.4772 6.74902 9.27155L6.78809 9.32233C6.8848 9.43418 7.02584 9.49999 7.17578 9.50006H8.82422C8.99569 9.49997 9.15584 9.41422 9.25098 9.27155L9.35449 9.12604C9.89229 8.41924 10.732 8.00006 11.627 8.00006H14.5L14.8232 8.00885C14.9404 8.38976 15 8.78611 15 9.18463V11.5001L14.9951 11.6797C14.9015 13.5292 13.3727 15.0001 11.5 15.0001H4.5C2.62729 15.0001 1.09845 13.5292 1.00488 11.6797L1 11.5001V9.18463C1.00002 8.88566 1.03339 8.58784 1.09961 8.29694L1.17676 8.00885L2.79004 2.76471C3.09264 1.78131 3.96362 1.09035 4.97559 1.00787L5.17969 1.00006H7.25ZM2.5 11.5001C2.50003 12.6046 3.39545 13.5001 4.5 13.5001H11.5C12.6045 13.5001 13.5 12.6046 13.5 11.5001V9.50006H11.627C11.2304 9.50006 10.8572 9.67386 10.6016 9.96979L10.499 10.1036C10.1257 10.6635 9.49722 11 8.82422 11.0001H7.17578C6.54495 11 5.95335 10.7043 5.57422 10.2061L5.50098 10.1036C5.24961 9.72653 4.8262 9.50006 4.37305 9.50006H2.5V11.5001Z" /><path d="M13.7197 1.21979C14.0126 0.926894 14.4874 0.926894 14.7803 1.21979C15.0731 1.51269 15.0731 1.98746 14.7803 2.28033L13.5605 3.50006L14.7803 4.71979C15.0732 5.01268 15.0732 5.48744 14.7803 5.78033C14.4874 6.07317 14.0126 6.07321 13.7197 5.78033L12.5 4.56061L11.2803 5.78033C10.9874 6.07321 10.5126 6.07317 10.2197 5.78033C9.92683 5.48744 9.92683 5.01268 10.2197 4.71979L11.4395 3.50006L10.2197 2.28033C9.92685 1.98746 9.92689 1.51269 10.2197 1.21979C10.5126 0.926894 10.9874 0.926894 11.2803 1.21979L12.5 2.43951L13.7197 1.21979Z" /></svg>
}

function PhoneSettingsInboxIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true" fill="currentColor" fillRule="evenodd" clipRule="evenodd"><path d="M8.00021 5.25C9.51887 5.25015 10.7502 6.48131 10.7502 8C10.7502 9.51869 9.51887 10.7499 8.00021 10.75C6.48142 10.75 5.25021 9.51878 5.25021 8C5.25021 6.48122 6.48142 5.25 8.00021 5.25ZM8.00021 6.75C7.30985 6.75 6.75021 7.30964 6.75021 8C6.75021 8.69036 7.30985 9.25 8.00021 9.25C8.69044 9.24985 9.25021 8.69027 9.25021 8C9.25021 7.30973 8.69044 6.75015 8.00021 6.75Z" /><path d="M8.4797 1C8.79032 1.00005 9.11521 1.07609 9.39571 1.28516C9.67641 1.49469 9.84241 1.78493 9.93087 2.08301L10.0764 2.57422C10.1033 2.66485 10.1451 2.71838 10.2268 2.76562L11.4192 3.4541C11.5007 3.50111 11.5678 3.51013 11.6594 3.48828L12.1584 3.36914C12.4605 3.2968 12.7939 3.29566 13.1154 3.43359C13.4372 3.57185 13.666 3.81486 13.8215 4.08398L14.302 4.91602C14.4573 5.18528 14.554 5.50472 14.5129 5.85254C14.4717 6.20036 14.303 6.48828 14.0891 6.71387L13.7365 7.08594C13.6717 7.15443 13.6467 7.21713 13.6467 7.31152V8.68848C13.6467 8.78287 13.6717 8.84557 13.7365 8.91406L14.0891 9.28613C14.303 9.51172 14.4717 9.79964 14.5129 10.1475C14.554 10.4953 14.4573 10.8147 14.302 11.084L13.8215 11.916C13.666 12.1851 13.4372 12.4281 13.1154 12.5664C12.7939 12.7043 12.4605 12.7032 12.1584 12.6309L11.6594 12.5117C11.5678 12.4899 11.5007 12.4989 11.4192 12.5459L10.2268 13.2344C10.1451 13.2816 10.1033 13.3352 10.0764 13.4258L9.93087 13.917C9.84241 14.2151 9.67641 14.5053 9.39571 14.7148C9.11521 14.9239 8.79032 15 8.4797 15H7.51974C7.20899 14.9999 6.8833 14.9242 6.60275 14.7148C6.32229 14.5053 6.15699 14.2149 6.06857 13.917L5.92306 13.4258C5.89612 13.3351 5.85359 13.2817 5.77169 13.2344L4.58028 12.5459C4.51915 12.5107 4.46564 12.4967 4.4045 12.501L4.33907 12.5117L3.84103 12.6309C3.53875 12.7032 3.20473 12.7047 2.88302 12.5664C2.56142 12.428 2.33239 12.1852 2.17696 11.916L1.69747 11.084C1.54209 10.8147 1.44542 10.4953 1.48653 10.1475C1.52769 9.79988 1.69567 9.51165 1.90939 9.28613L2.26192 8.91406C2.327 8.84544 2.35271 8.783 2.35275 8.68848V7.31152C2.35271 7.217 2.327 7.15455 2.26192 7.08594L1.90939 6.71387C1.69567 6.48835 1.52769 6.20012 1.48653 5.85254C1.44542 5.50474 1.54209 5.18527 1.69747 4.91602L2.17696 4.08398C2.33239 3.81484 2.56142 3.57196 2.88302 3.43359C3.20473 3.29533 3.53875 3.29676 3.84103 3.36914L4.33907 3.48828L4.4045 3.49902C4.46564 3.50331 4.51915 3.48931 4.58028 3.4541L5.77169 2.76562C5.85359 2.71834 5.89612 2.66492 5.92306 2.57422L6.06857 2.08301C6.15699 1.78507 6.32229 1.49467 6.60275 1.28516C6.8833 1.07582 7.20899 1.0001 7.51974 1H8.4797ZM7.50607 2.50977L7.36056 3.00098C7.2244 3.45982 6.93621 3.82513 6.52169 4.06445L5.33028 4.75293C4.9675 4.96225 4.56891 5.03929 4.16329 4.98047L3.98946 4.94727L3.49142 4.82812C3.48792 4.82734 3.48373 4.82682 3.48067 4.82617C3.47921 4.82856 3.47735 4.83128 3.47579 4.83398L2.9963 5.66602C2.99471 5.66872 2.99276 5.67137 2.99142 5.67383C2.9935 5.67614 2.99584 5.67906 2.99825 5.68164L3.35079 6.05371C3.68013 6.401 3.8527 6.83292 3.85275 7.31152V8.68848C3.8527 9.16708 3.68013 9.599 3.35079 9.94629L2.99825 10.3184C2.99599 10.3207 2.99337 10.323 2.99142 10.3252C2.99288 10.3279 2.99458 10.331 2.9963 10.334L3.47579 11.166C3.4772 11.1685 3.47931 11.1707 3.48067 11.1729C3.48367 11.1722 3.48798 11.1726 3.49142 11.1719L3.98946 11.0527L4.16329 11.0195C4.56891 10.9607 4.96751 11.0378 5.33028 11.2471L6.52169 11.9355C6.93621 12.1749 7.2244 12.5402 7.36056 12.999L7.50607 13.4902L7.509 13.499C7.51207 13.4991 7.51622 13.4999 7.51974 13.5H8.4797C8.48257 13.5 8.48588 13.4992 8.48849 13.499C8.48939 13.4962 8.49141 13.4934 8.49239 13.4902L8.6379 12.999L8.6965 12.8311C8.84844 12.4508 9.11439 12.1449 9.47677 11.9355L10.6692 11.2471C11.0834 11.008 11.5439 10.9414 12.009 11.0527L12.508 11.1719C12.5108 11.1725 12.5142 11.1724 12.5168 11.1729C12.5182 11.1705 12.5211 11.1686 12.5227 11.166L13.0022 10.334C13.0038 10.3311 13.0047 10.3278 13.0061 10.3252C13.0043 10.3232 13.0022 10.3205 13.0002 10.3184L12.6477 9.94629C12.3186 9.59908 12.1467 9.16687 12.1467 8.68848V7.31152C12.1467 6.83313 12.3186 6.40092 12.6477 6.05371L13.0002 5.68164C13.0024 5.67931 13.0042 5.67603 13.0061 5.67383C13.0048 5.67147 13.0036 5.66858 13.0022 5.66602L12.5227 4.83398C12.521 4.83114 12.5183 4.82869 12.5168 4.82617C12.5142 4.82674 12.5109 4.82744 12.508 4.82812L12.009 4.94727C11.5439 5.05858 11.0834 4.99203 10.6692 4.75293L9.47677 4.06445C9.11439 3.8551 8.84844 3.54923 8.6965 3.16895L8.6379 3.00098L8.49239 2.50977C8.49133 2.50626 8.48948 2.50306 8.48849 2.5H7.509L7.50607 2.50977Z" /></svg>
}

function SettingsInboxIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8.58 1a.5.5 0 0 1 .49.42l.25 1.48a.49.49 0 0 0 .33.38c.18.059.353.132.52.22a.49.49 0 0 0 .51 0l1.22-.87a.5.5 0 0 1 .64 0l.82.82a.5.5 0 0 1 .05.64l-.87 1.22a.49.49 0 0 0 0 .51c.088.167.161.34.22.52a.49.49 0 0 0 .38.33l1.48.25a.5.5 0 0 1 .42.49v1.17a.5.5 0 0 1-.42.49l-1.48.25a.49.49 0 0 0-.38.33 3.38 3.38 0 0 1-.22.52.49.49 0 0 0 0 .51l.87 1.22a.5.5 0 0 1-.05.64l-.82.82a.5.5 0 0 1-.64.05l-1.22-.87a.49.49 0 0 0-.51 0 3.38 3.38 0 0 1-.52.22.49.49 0 0 0-.33.38l-.25 1.48a.5.5 0 0 1-.49.42H7.42a.5.5 0 0 1-.49-.42l-.25-1.52a.49.49 0 0 0-.33-.38 3.38 3.38 0 0 1-.52-.22.49.49 0 0 0-.51 0l-1.22.87a.5.5 0 0 1-.64-.05l-.82-.82a.5.5 0 0 1 0-.64l.87-1.22a.49.49 0 0 0 0-.51 3.38 3.38 0 0 1-.22-.52.49.49 0 0 0-.38-.33l-1.49-.21A.5.5 0 0 1 1 8.58V7.42a.5.5 0 0 1 .42-.49l1.48-.25a.49.49 0 0 0 .38-.33 3.38 3.38 0 0 1 .22-.52.49.49 0 0 0 0-.51L2.59 4.1a.5.5 0 0 1 0-.64l.82-.82a.5.5 0 0 1 .64 0l1.22.87a.49.49 0 0 0 .51 0 3.38 3.38 0 0 1 .52-.22.49.49 0 0 0 .33-.38l.3-1.49A.5.5 0 0 1 7.42 1h1.16ZM8 6a2 2 0 1 0 0 4 2 2 0 0 0 0-4Z" fill="currentColor" fillRule="evenodd" /></svg>
}

function ChevronDownIcon() {
  return <svg viewBox="0 0 9 5" aria-hidden="true"><path d="M1.1.8 4.5 4.2 7.9.8" fill="none" stroke="currentColor" strokeWidth="1.1" strokeLinecap="round" strokeLinejoin="round" /></svg>
}

function InboxTrayIllustration() {
  return <svg className="flow-inbox__tray" viewBox="0 0 78 80" aria-hidden="true">
    <path stroke="var(--inbox-tray-outer)" strokeWidth="1.5" d="M10.4 9.11A10 10 0 0 1 20.22 1h37.56a10 10 0 0 1 9.82 8.11l8.11 42.2a10 10 0 0 1-9.82 11.9H54.7a6.36 6.36 0 0 0-5.65 3.45 6.36 6.36 0 0 1-5.66 3.45H34.6a6.36 6.36 0 0 1-5.66-3.45 6.36 6.36 0 0 0-5.65-3.46H12.1a10 10 0 0 1-9.8-11.89l8.11-42.2Z" />
    <path stroke="var(--inbox-tray-inner)" strokeWidth="1.5" d="M14.3 9.03a5 5 0 0 1 4.91-4.08H58.8a5 5 0 0 1 4.91 4.08l8.07 43.22a6 6 0 0 1-5.9 7.1H52.76a5.72 5.72 0 0 0-5.24 3.41 5.72 5.72 0 0 1-5.23 3.42h-6.58a5.72 5.72 0 0 1-5.23-3.42 5.72 5.72 0 0 0-5.24-3.4h-13.1a6 6 0 0 1-5.9-7.1L14.3 9.02Z" />
    <path stroke="var(--inbox-tray-outer)" strokeWidth="1.5" d="m2.36 55.6 3.2 14.06A12 12 0 0 0 17.26 79h43.48a12 12 0 0 0 11.7-9.34l3.2-14.06" />
  </svg>
}
