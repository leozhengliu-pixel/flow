import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Select from '@radix-ui/react-select'
import type { ButtonHTMLAttributes, CSSProperties, KeyboardEvent as ReactKeyboardEvent, PointerEvent, ReactNode } from 'react'
import { forwardRef, useEffect, useRef, useState } from 'react'

import { InboxFilterBuilder, type InboxFilterCondition, type InboxFilterOptions } from './inbox-filter-builder'
import './inbox.css'
import { CheckIcon, DisplayIcon, FilterIcon, SidebarIcon } from '@/components/ui/view-action-icons'

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
  onOpenSettings?: () => void
  onOpenSidebar?: () => void
  activeTab?: InboxTab
  onTabChange?: (tab: InboxTab) => void
  tabCounts?: Partial<Record<InboxTab, number>>
}

const INBOX_LIST_WIDTH_KEY = 'flow.inbox.list-width'
const DEFAULT_LIST_WIDTH = 300
const MIN_LIST_WIDTH = 300
const MIN_DETAIL_WIDTH = 608

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
  try {
    const width = Number(globalThis.localStorage?.getItem(INBOX_LIST_WIDTH_KEY))
    return Number.isFinite(width) && width > 0 ? width : DEFAULT_LIST_WIDTH
  } catch {
    return DEFAULT_LIST_WIDTH
  }
}

function persistInboxListWidth(width: number) {
  try {
    globalThis.localStorage?.setItem(INBOX_LIST_WIDTH_KEY, String(Math.round(width)))
  } catch {
    // Preferences are best-effort in private browsing.
  }
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
          onClick={onOpenSidebar}
        >
          <SidebarIcon />
        </button>
      ) : null}
      <div className="flow-inbox__header-title-actions">
        <h2>Inbox</h2>
        <NotificationActionsMenu
          pending={bulkPending}
          onDeleteAll={onDeleteAll}
          onDeleteAllRead={onDeleteAllRead}
          onDeleteAllReadCompleted={onDeleteAllReadCompleted}
          onOpenSettings={onOpenSettings}
        />
      </div>
      <div className="flow-inbox__header-spacer" />
      <div className="flow-inbox__header-controls">
        <button
          aria-label="Show unreads only"
          aria-pressed={!displayOptions.showRead}
          className="flow-inbox__icon-button"
          data-active={!displayOptions.showRead || undefined}
          onClick={() => onDisplayOptionsChange({ ...displayOptions, showRead: !displayOptions.showRead })}
          type="button"
        ><UnreadFilterIcon /></button>
        <InboxFilterBuilder
          trigger={<IconButton label="Add filter" count={filters?.length ?? 0}><FilterIcon /></IconButton>}
          filters={filters ?? []}
          options={filterOptions}
          onFiltersChange={nextFilters => onFiltersChange?.(nextFilters)}
        />
        <DisplayOptionsMenu value={displayOptions} onChange={onDisplayOptionsChange} />
      </div>
    </header>
  )
}

function UnreadFilterIcon() {
  return <svg aria-hidden="true" viewBox="0 0 16 16"><path fill="currentColor" fillRule="evenodd" d="M7.25 1C7.66421 1 8 1.33579 8 1.75C8 2.16421 7.66421 2.5 7.25 2.5H5.17969C4.79573 2.50003 4.45093 2.71902 4.28418 3.05469L4.22363 3.20605L2.74902 8H4.37305C5.32773 8 6.21946 8.47714 6.74902 9.27148L6.78809 9.32227C6.8848 9.43414 7.02582 9.49992 7.17578 9.5H8.82422C8.99571 9.49991 9.15584 9.41418 9.25098 9.27148L9.35449 9.12598C9.89229 8.41918 10.732 8 11.627 8H14.8115C14.8169 8.0002 14.8215 8.00369 14.8232 8.00879C14.9404 8.38972 15 8.78603 15 9.18457V11.5L14.9951 11.6797C14.9016 13.5292 13.3727 15 11.5 15H4.5C2.62727 15 1.09842 13.5292 1.00488 11.6797L1 11.5V9.18457C1.00002 8.88558 1.03338 8.5878 1.09961 8.29688L1.17676 8.00879L2.79004 2.76465C3.09264 1.78125 3.96362 1.09029 4.97559 1.00781L5.17969 1H7.25ZM2.5 11.5C2.5 12.6046 3.39543 13.5 4.5 13.5H11.5C12.6046 13.5 13.5 12.6046 13.5 11.5V9.5H11.627C11.2304 9.5 10.8572 9.6738 10.6016 9.96973L10.499 10.1035C10.1257 10.6635 9.49724 10.9999 8.82422 11H7.17578C6.54494 10.9999 5.95335 10.7043 5.57422 10.2061L5.50098 10.1035C5.24961 9.72647 4.8262 9.5 4.37305 9.5H2.5V11.5Z" /><path fill="currentColor" d="M12.5 1C13.8807 1 15 2.11929 15 3.5C15 4.88071 13.8807 6 12.5 6C11.1193 6 10 4.88071 10 3.5C10 2.11929 11.1193 1 12.5 1Z" /></svg>
}

function NotificationActionsMenu({
  pending = false,
  onDeleteAll,
  onDeleteAllRead,
  onDeleteAllReadCompleted,
  onOpenSettings,
}: Pick<
  InboxPageShellProps,
  'onDeleteAll' | 'onDeleteAllRead' | 'onDeleteAllReadCompleted' | 'onOpenSettings'
> & { pending?: boolean }) {
  useInboxShortcut('Backspace', event => {
    if (!event.shiftKey) return false
    onDeleteAllRead()
    return true
  })

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
          <InboxMenuItem disabled={pending} icon={<DeleteInboxIcon />} onSelect={onDeleteAll}>
            Delete all
          </InboxMenuItem>
          <InboxMenuItem disabled={pending} icon={<DeleteInboxIcon />} shortcut="⇧⌫" onSelect={onDeleteAllRead}>
            Delete all read
          </InboxMenuItem>
          <InboxMenuItem disabled={pending} icon={<DeleteInboxIcon />} onSelect={onDeleteAllReadCompleted}>
            Delete all completed
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

function DisplayOptionsMenu({
  value,
  onChange,
}: {
  value: InboxDisplayOptions
  onChange: (value: InboxDisplayOptions) => void
}) {
  const update = (change: Partial<InboxDisplayOptions>) => onChange({ ...value, ...change })
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <IconButton label="Display options">
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
          <div className="flow-inbox-menu__display-row">
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
      {shortcut ? <kbd className="flow-inbox-menu__shortcut">{shortcut}</kbd> : null}
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
  return target.isContentEditable || target.matches('input, textarea, select, [role="textbox"], [role="searchbox"]')
}

interface IconButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  label: string
  count?: number
  children: ReactNode
}

const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton({ label, count, children, ...buttonProps }, ref) {
  return (
    <button {...buttonProps} ref={ref} className="flow-inbox__icon-button" type="button" aria-label={label}>
      {children}
      {count ? <span className="flow-inbox__filter-count">{count}</span> : null}
    </button>
  )
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


function DeleteInboxIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path fillRule="evenodd" clipRule="evenodd" d="M7.25 1a.75.75 0 0 1 0 1.5H5.18a1 1 0 0 0-.956.706L2.75 8h1.623c.955 0 1.846.477 2.376 1.272a.51.51 0 0 0 .427.228h1.648a.51.51 0 0 0 .427-.228A2.856 2.856 0 0 1 11.627 8H14.5l.323.009c.117.38.177.777.177 1.176V11.5a3.5 3.5 0 0 1-3.5 3.5h-7A3.5 3.5 0 0 1 1 11.5V9.185c0-.299.033-.597.1-.888l.077-.288L2.79 2.765A2.5 2.5 0 0 1 5.18 1h2.07ZM2.5 9.5v2a2 2 0 0 0 2 2h7a2 2 0 0 0 2-2v-2h-1.873c-.397 0-.77.174-1.025.47l-.103.134A2.014 2.014 0 0 1 8.824 11H7.176a2.014 2.014 0 0 1-1.675-.896l-.103-.134a1.356 1.356 0 0 0-1.025-.47H2.5Zm11.22-8.28a.75.75 0 0 1 1.06 1.06L13.561 3.5l1.22 1.22a.75.75 0 1 1-1.061 1.06L12.5 4.561l-1.22 1.22a.75.75 0 1 1-1.06-1.061l1.219-1.22-1.22-1.22a.75.75 0 1 1 1.061-1.06l1.22 1.219 1.22-1.22Z" /></svg>
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
