import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Select from '@radix-ui/react-select'
import type { ButtonHTMLAttributes, CSSProperties, KeyboardEvent as ReactKeyboardEvent, PointerEvent, ReactNode } from 'react'
import { forwardRef, useEffect, useRef, useState } from 'react'

import { InboxFilterBuilder, type InboxFilterCondition, type InboxFilterOptions, type InboxFilterProperty } from './inbox-filter-builder'
import './inbox.css'

export type InboxFilterKind = InboxFilterProperty

export type InboxOrdering = 'newest' | 'oldest' | 'priority'

export interface InboxDisplayOptions {
  ordering: InboxOrdering
  showSnoozed: boolean
  showRead: boolean
  showUnreadFirst: boolean
}

export interface InboxPageShellProps {
  children: ReactNode
  detail?: ReactNode
  /** @deprecated Replaced by `filters`; retained while callers migrate. */
  filterCount?: number
  filters?: InboxFilterCondition[]
  filterOptions?: InboxFilterOptions
  filterHiddenCount?: number
  bulkPending?: boolean
  onFiltersChange?: (filters: InboxFilterCondition[]) => void
  displayOptions: InboxDisplayOptions
  /** @deprecated Replaced by `onFiltersChange`; retained while callers migrate. */
  onAddFilter?: (filter: InboxFilterKind) => void
  onDisplayOptionsChange: (options: InboxDisplayOptions) => void
  onDeleteAll: () => void
  onDeleteAllRead: () => void
  onDeleteAllReadCompleted: () => void
  onOpenSidebar?: () => void
}

const INBOX_LIST_WIDTH_KEY = 'flow.inbox.list-width'
const DEFAULT_LIST_WIDTH = 300
const MIN_LIST_WIDTH = 300
const MIN_DETAIL_WIDTH = 608

export function InboxPageShell({
  children,
  detail,
  filterCount = 0,
  filters,
  filterOptions,
  filterHiddenCount = 0,
  bulkPending = false,
  onFiltersChange,
  displayOptions,
  onAddFilter,
  onDisplayOptionsChange,
  onDeleteAll,
  onDeleteAllRead,
  onDeleteAllReadCompleted,
  onOpenSidebar,
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
          filterCount={filterCount}
          filters={filters}
          filterOptions={filterOptions}
          bulkPending={bulkPending}
          onFiltersChange={onFiltersChange}
          displayOptions={displayOptions}
          onAddFilter={onAddFilter}
          onDisplayOptionsChange={onDisplayOptionsChange}
          onDeleteAll={onDeleteAll}
          onDeleteAllRead={onDeleteAllRead}
          onDeleteAllReadCompleted={onDeleteAllReadCompleted}
          onOpenSidebar={onOpenSidebar}
        />
        <div className="flow-inbox__list">
          {children}
        </div>
        {filterHiddenCount > 0 ? <div className="flow-inbox__filter-footer" role="status">
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
  filterCount = 0,
  filters,
  filterOptions,
  bulkPending = false,
  onFiltersChange,
  displayOptions,
  onAddFilter,
  onDisplayOptionsChange,
  onDeleteAll,
  onDeleteAllRead,
  onDeleteAllReadCompleted,
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
        />
      </div>
      <div className="flow-inbox__header-spacer" />
      <div className="flow-inbox__header-controls">
        <InboxFilterBuilder
          trigger={<IconButton label="Add filter" count={filters?.length ?? filterCount}><FilterIcon /></IconButton>}
          filters={filters ?? []}
          options={filterOptions}
          onFiltersChange={nextFilters => {
            if (onFiltersChange) {
              onFiltersChange(nextFilters)
              return
            }
            nextFilters.forEach(filter => onAddFilter?.(filter.property))
          }}
        />
        <DisplayOptionsMenu value={displayOptions} onChange={onDisplayOptionsChange} />
      </div>
    </header>
  )
}

function NotificationActionsMenu({
  pending = false,
  onDeleteAll,
  onDeleteAllRead,
  onDeleteAllReadCompleted,
}: Pick<
  InboxPageShellProps,
  'onDeleteAll' | 'onDeleteAllRead' | 'onDeleteAllReadCompleted'
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
        <DropdownMenu.Content
          className="flow-inbox-menu flow-inbox-menu--actions"
          side="bottom"
          align="start"
          sideOffset={4}
        >
          <MenuItem disabled={pending} icon={<DeleteInboxIcon />} onSelect={onDeleteAll}>
            Delete all
          </MenuItem>
          <MenuItem disabled={pending} icon={<DeleteInboxIcon />} shortcut="⇧⌫" onSelect={onDeleteAllRead}>
            Delete all read
          </MenuItem>
          <MenuItem disabled={pending} icon={<DeleteInboxIcon />} onSelect={onDeleteAllReadCompleted}>
            Delete all read for completed issues
          </MenuItem>
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
        <DropdownMenu.Content
          className="flow-inbox-menu flow-inbox-menu--display"
          side="bottom"
          align="end"
          sideOffset={4}
        >
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
              checked={value.showRead}
              onCheckedChange={(checked) => update({ showRead: checked === true })}
            >
              Show read
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

function OrderingSelect({ value, onChange }: { value: InboxOrdering; onChange: (value: InboxOrdering) => void }) {
  const labels: Record<InboxOrdering, string> = { newest: 'Newest', oldest: 'Oldest', priority: 'Priority' }
  return (
    <Select.Root value={value} onValueChange={nextValue => onChange(nextValue as InboxOrdering)}>
      <Select.Trigger className="flow-inbox-ordering-select">
        <Select.Value>{labels[value]}</Select.Value>
        <Select.Icon><ChevronDownIcon /></Select.Icon>
      </Select.Trigger>
      <Select.Portal>
        <Select.Content className="flow-inbox-ordering-options" position="popper" sideOffset={4} align="end">
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

function MenuItem({
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

function FilterIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path fillRule="evenodd" clipRule="evenodd" d="M14.25 3a.75.75 0 0 1 0 1.5H1.75a.75.75 0 0 1 0-1.5h12.5ZM4 8a.75.75 0 0 1 .75-.75h6.5a.75.75 0 0 1 0 1.5h-6.5A.75.75 0 0 1 4 8Zm2.75 3.5a.75.75 0 0 0 0 1.5h2.5a.75.75 0 0 0 0-1.5h-2.5Z" /></svg>
}

function DisplayIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path fillRule="evenodd" clipRule="evenodd" d="M7 2.5A2.5 2.5 0 0 1 9.385 4.25h5.365a.75.75 0 0 1 0 1.5H9.385a2.501 2.501 0 0 1-4.77 0H2.25a.75.75 0 0 1 0-1.5h2.365A2.5 2.5 0 0 1 7 2.5ZM7 4a1 1 0 1 0 0 2 1 1 0 0 0 0-2Zm3 9.5a2.5 2.5 0 0 1-2.385-1.75H2.25a.75.75 0 0 1 0-1.5h5.365a2.501 2.501 0 0 1 4.77 0h2.365a.75.75 0 0 1 0 1.5h-2.365A2.5 2.5 0 0 1 10 13.5Zm0-4.5a2 2 0 1 0 0 4 2 2 0 0 0 0-4Zm0 1.5a.5.5 0 1 0 0 1 .5.5 0 0 0 0-1Z" /></svg>
}

function SidebarIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path fillRule="evenodd" clipRule="evenodd" d="M3.5 2.5h9A1.5 1.5 0 0 1 14 4v8a1.5 1.5 0 0 1-1.5 1.5h-9A1.5 1.5 0 0 1 2 12V4a1.5 1.5 0 0 1 1.5-1.5ZM6 4H3.5v8H6V4Zm1.5 0v8h5V4h-5Z" /></svg>
}

function DeleteInboxIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path fillRule="evenodd" clipRule="evenodd" d="M7.25 1a.75.75 0 0 1 0 1.5H5.18a1 1 0 0 0-.956.706L2.75 8h1.623c.955 0 1.846.477 2.376 1.272a.51.51 0 0 0 .427.228h1.648a.51.51 0 0 0 .427-.228A2.856 2.856 0 0 1 11.627 8H14.5l.323.009c.117.38.177.777.177 1.176V11.5a3.5 3.5 0 0 1-3.5 3.5h-7A3.5 3.5 0 0 1 1 11.5V9.185c0-.299.033-.597.1-.888l.077-.288L2.79 2.765A2.5 2.5 0 0 1 5.18 1h2.07ZM2.5 9.5v2a2 2 0 0 0 2 2h7a2 2 0 0 0 2-2v-2h-1.873c-.397 0-.77.174-1.025.47l-.103.134A2.014 2.014 0 0 1 8.824 11H7.176a2.014 2.014 0 0 1-1.675-.896l-.103-.134a1.356 1.356 0 0 0-1.025-.47H2.5Zm11.22-8.28a.75.75 0 0 1 1.06 1.06L13.561 3.5l1.22 1.22a.75.75 0 1 1-1.061 1.06L12.5 4.561l-1.22 1.22a.75.75 0 1 1-1.06-1.061l1.219-1.22-1.22-1.22a.75.75 0 1 1 1.061-1.06l1.22 1.219 1.22-1.22Z" /></svg>
}

function ChevronDownIcon() {
  return <svg viewBox="0 0 9 5" aria-hidden="true"><path d="M1.1.8 4.5 4.2 7.9.8" fill="none" stroke="currentColor" strokeWidth="1.1" strokeLinecap="round" strokeLinejoin="round" /></svg>
}

function CheckIcon() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path d="m3.5 8.2 2.7 2.7 6.3-6.3" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" /></svg>
}

function InboxTrayIllustration() {
  return <svg className="flow-inbox__tray" viewBox="0 0 100 100" aria-hidden="true"><path d="M23 17.5h54a7 7 0 0 1 6.8 5.4l9.2 39.7v15.9A8.5 8.5 0 0 1 84.5 87h-69A8.5 8.5 0 0 1 7 78.5V62.6l9.2-39.7a7 7 0 0 1 6.8-5.4Z" /><path d="M7 62.5h25.2a7 7 0 0 1 6.2 3.8l1.2 2.3a7 7 0 0 0 6.2 3.8h8.4a7 7 0 0 0 6.2-3.8l1.2-2.3a7 7 0 0 1 6.2-3.8H93" /></svg>
}
