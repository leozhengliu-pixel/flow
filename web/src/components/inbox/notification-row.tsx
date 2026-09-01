import * as ContextMenu from '@radix-ui/react-context-menu'
import * as Popover from '@radix-ui/react-popover'
import { useCallback, useRef, useState, type CSSProperties, type FocusEvent, type FormEvent, type KeyboardEvent, type ReactNode } from 'react'

import { StatusIcon } from '@/components/issue/issue-icons'
import { DateTimeControl } from '@/components/ui/date-time-control'
import type { WorkflowState } from '@/types/flow'

import styles from './notification-row.module.css'
import './inbox-date-control.css'

export type InboxNotificationKind = 'comment' | 'assignment' | 'mention' | 'status' | 'project' | 'generic'
export type InboxSnoozePreset = 'hour' | 'tomorrow' | 'nextWeek' | 'month' | {
  kind: 'custom'
  snoozedUntil: string
}

export interface InboxNotificationRowData {
  id: string
  href?: string
  actorId?: string
  actor: string
  actorAvatarUrl?: string
  actorInitials?: string
  kind: InboxNotificationKind
  identifier: string
  title: string
  body: string
  timeLabel: string
  timestamp: string
  read: boolean
  favorite?: boolean
  issueState?: Pick<WorkflowState, 'id' | 'name' | 'color' | 'type'>
}

export interface InboxNotificationRowProps {
  notification: InboxNotificationRowData
  active?: boolean
  selected?: boolean
  disabled?: boolean
  pending?: boolean
  onOpen: (notification: InboxNotificationRowData) => void | Promise<void>
  onReadChange: (notification: InboxNotificationRowData, read: boolean) => void | Promise<void>
  onDelete: (notification: InboxNotificationRowData) => void | Promise<void>
  onSnooze: (notification: InboxNotificationRowData, preset: InboxSnoozePreset) => void | Promise<void>
  onFavoriteChange?: (notification: InboxNotificationRowData, favorite: boolean) => void | Promise<void>
  onCopyLink?: (notification: InboxNotificationRowData) => void | Promise<void>
  onCopyIdentifier?: (notification: InboxNotificationRowData) => void | Promise<void>
  onMoveFocus?: (direction: -1 | 1, notification: InboxNotificationRowData) => void
  onFocus?: (notification: InboxNotificationRowData) => void
  onBlur?: (notification: InboxNotificationRowData) => void
}

export function InboxNotificationRow(props: InboxNotificationRowProps) {
  const { notification, active = false, selected = false, disabled = false, pending = false } = props
  const rowRef = useRef<HTMLDivElement>(null)
  const [keyboardSnoozeOpen, setKeyboardSnoozeOpen] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)

  // Page-level callers own the optimistic mutation and rollback. The row also
  // accepts direct promise callbacks for isolated use (stories and tests).
  const invoke = useCallback((action: () => void | Promise<void>) => {
    setActionError(null)
    try {
      const result = action()
      if (isPromise(result)) {
        void result.catch((cause) => {
          setActionError(cause instanceof Error ? cause.message : 'Notification action failed.')
        })
      }
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : 'Notification action failed.')
    }
  }, [])

  const open = () => {
    if (disabled) return
    invoke(() => props.onOpen(notification))
  }
  const toggleRead = () => {
    if (disabled) return
    invoke(() => props.onReadChange(notification, !notification.read))
  }
  const toggleFavorite = () => {
    if (disabled || !props.onFavoriteChange) return
    invoke(() => props.onFavoriteChange?.(notification, !notification.favorite))
  }
  const deleteNotification = () => {
    if (disabled) return
    invoke(() => props.onDelete(notification))
  }
  const snoozeNotification = (preset: InboxSnoozePreset) => {
    if (disabled) return
    setKeyboardSnoozeOpen(false)
    invoke(() => props.onSnooze(notification, preset))
  }

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (disabled || event.currentTarget !== event.target) return
    const key = event.key.toLowerCase()
    if (event.altKey || event.ctrlKey || event.metaKey) {
      if (event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey && (event.code === 'KeyF' || key === 'f')) {
        event.preventDefault()
        toggleFavorite()
      }
      return
    }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      props.onMoveFocus?.(event.key === 'ArrowDown' ? 1 : -1, notification)
      return
    }
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      open()
      return
    }
    if (key === 'u') {
      event.preventDefault()
      toggleRead()
      return
    }
    if (key === 'h') {
      event.preventDefault()
      setActionError(null)
      setKeyboardSnoozeOpen(true)
      return
    }
    if (event.key === 'Backspace' || event.key === 'Delete') {
      event.preventDefault()
      deleteNotification()
    }
  }

  const handleBlur = (event: FocusEvent<HTMLDivElement>) => {
    if (!event.currentTarget.contains(event.relatedTarget)) props.onBlur?.(notification)
  }

  return (
    <Popover.Root open={keyboardSnoozeOpen} onOpenChange={setKeyboardSnoozeOpen}>
      <ContextMenu.Root>
        <Popover.Anchor asChild>
          <ContextMenu.Trigger asChild disabled={disabled}>
            <div
              ref={rowRef}
              className="flow-inbox-row"
              role="link"
              aria-label={`${notification.actor} ${notification.identifier} ${notification.title} ${notification.body} ${notification.timeLabel}`}
              aria-current={active ? 'page' : undefined}
              aria-disabled={disabled || undefined}
              aria-busy={pending || undefined}
              aria-describedby={actionError ? `${notification.id}-action-error` : undefined}
              aria-keyshortcuts="Enter Space U H Alt+F Backspace Delete ArrowDown ArrowUp"
              data-active={active}
              data-selected={selected}
              data-read={notification.read}
              data-action-error={actionError ? 'true' : undefined}
              tabIndex={disabled ? -1 : 0}
              onClick={open}
              onKeyDown={handleKeyDown}
              onFocus={() => props.onFocus?.(notification)}
              onBlur={handleBlur}
            >
              <div className="flow-inbox-row__inner">
                <ActorVisual notification={notification} />
                <div className="flow-inbox-row__content">
                  <div className="flow-inbox-row__headline" title={notification.title}>
                    <span className="flow-inbox-row__identifier">{notification.identifier}</span>
                    <span className="flow-inbox-row__title">{notification.title}</span>
                    {notification.issueState ? <span aria-label={notification.issueState.name} className="flow-inbox-row__issue-state" title={notification.issueState.name}><StatusIcon size={14} state={notification.issueState} /></span> : null}
                  </div>
                  <div className="flow-inbox-row__summary">
                    <span className="flow-inbox-row__body">{notification.body}</span>
                    <time title={notification.timestamp}>{notification.timeLabel}</time>
                  </div>
                </div>
              </div>
              {pending ? <span className="flow-inbox-row__pending" aria-hidden="true" /> : null}
              {actionError ? <span id={`${notification.id}-action-error`} className={styles.actionError} role="alert">{actionError}</span> : null}
            </div>
          </ContextMenu.Trigger>
        </Popover.Anchor>
        <NotificationContextMenu
          {...props}
          onReadChange={() => toggleRead()}
          onDelete={deleteNotification}
          onSnooze={(_, preset) => snoozeNotification(preset)}
          onFavoriteChange={props.onFavoriteChange ? () => toggleFavorite() : undefined}
          onCopyLink={props.onCopyLink ? () => invoke(() => props.onCopyLink?.(notification)) : undefined}
          onCopyIdentifier={props.onCopyIdentifier ? () => invoke(() => props.onCopyIdentifier?.(notification)) : undefined}
        />
      </ContextMenu.Root>
      <Popover.Portal>
        <Popover.Content
          className="flow-inbox-menu flow-inbox-snooze-menu"
          aria-label="Snooze notification"
          side="right"
          align="start"
          sideOffset={8}
          collisionPadding={12}
          onOpenAutoFocus={(event) => {
            event.preventDefault()
            window.requestAnimationFrame(() => document.querySelector<HTMLElement>(`[data-snooze-option="${cssAttributeValue(notification.id)}"]`)?.focus())
          }}
          onEscapeKeyDown={(event) => {
            event.preventDefault()
            event.stopPropagation()
            setKeyboardSnoozeOpen(false)
          }}
          onCloseAutoFocus={(event) => {
            event.preventDefault()
            window.requestAnimationFrame(() => rowRef.current?.focus())
          }}
        >
          <KeyboardSnoozeMenu
            notification={notification}
            disabled={disabled}
            onSnooze={snoozeNotification}
          />
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}

function ActorVisual({ notification }: { notification: InboxNotificationRowData }) {
  return (
    <div className="flow-inbox-row__actor">
      {notification.actorAvatarUrl ? (
        <img className="flow-inbox-row__avatar" src={notification.actorAvatarUrl} alt="" aria-label={notification.actor} />
      ) : (
        <span className="flow-inbox-row__avatar flow-inbox-row__initials" aria-label={notification.actor} style={{ '--flow-inbox-avatar': actorColor(notification.actorId ?? notification.actor) } as CSSProperties}>
          {notification.actorInitials ?? initials(notification.actor)}
        </span>
      )}
      <span className="flow-inbox-row__kind" aria-hidden="true">
        <KindIcon kind={notification.kind} />
      </span>
    </div>
  )
}

function NotificationContextMenu(props: InboxNotificationRowProps) {
  const { notification, disabled = false } = props
  return (
    <ContextMenu.Portal>
      <ContextMenu.Content className="flow-inbox-menu flow-inbox-row-menu" aria-label="Notification actions" onEscapeKeyDown={(event) => event.stopPropagation()}>
        <RowMenuItem
          shortcut="U"
          icon={<UnreadIcon />}
          disabled={disabled}
          onSelect={() => props.onReadChange(notification, !notification.read)}
        >
          {notification.read ? 'Mark as unread' : 'Mark as read'}
        </RowMenuItem>
        <RowMenuItem shortcut="⌫" icon={<DeleteIcon />} disabled={disabled} onSelect={() => props.onDelete(notification)}>
          Delete notification
        </RowMenuItem>
        <ContextMenu.Sub>
          <ContextMenu.SubTrigger className="flow-inbox-menu__item" disabled={disabled}>
            <span className="flow-inbox-menu__item-icon"><SnoozeIcon /></span>
            <span className="flow-inbox-menu__item-label">Snooze</span>
            <kbd className="flow-inbox-menu__shortcut">H</kbd>
            <span className="flow-inbox-menu__trailing">▶</span>
          </ContextMenu.SubTrigger>
          <ContextMenu.Portal>
            <ContextMenu.SubContent className="flow-inbox-menu flow-inbox-snooze-menu" sideOffset={4}>
              <RowSnoozeMenu notification={notification} disabled={disabled} onSnooze={props.onSnooze} />
            </ContextMenu.SubContent>
          </ContextMenu.Portal>
        </ContextMenu.Sub>
        <ContextMenu.Separator className="flow-inbox-menu__separator flow-inbox-row-menu__separator" />
        {props.onFavoriteChange ? (
          <RowMenuItem
            shortcut="⌥F"
            icon={<FavoriteIcon />}
            disabled={disabled}
            onSelect={() => props.onFavoriteChange?.(notification, !notification.favorite)}
          >
            {notification.favorite ? 'Unfavorite' : 'Favorite'}
          </RowMenuItem>
        ) : null}
        {props.onCopyLink || props.onCopyIdentifier ? (
          <ContextMenu.Sub>
            <ContextMenu.SubTrigger className="flow-inbox-menu__item" disabled={disabled}>
              <span className="flow-inbox-menu__item-icon"><CopyIcon /></span>
              <span className="flow-inbox-menu__item-label">Copy</span>
              <span className="flow-inbox-menu__trailing">▶</span>
            </ContextMenu.SubTrigger>
            <ContextMenu.Portal>
              <ContextMenu.SubContent className="flow-inbox-menu flow-inbox-copy-menu" sideOffset={4}>
                {props.onCopyLink ? <RowMenuItem disabled={disabled} onSelect={() => props.onCopyLink?.(notification)}>Copy link</RowMenuItem> : null}
                {props.onCopyIdentifier ? <RowMenuItem disabled={disabled} onSelect={() => props.onCopyIdentifier?.(notification)}>Copy issue ID</RowMenuItem> : null}
              </ContextMenu.SubContent>
            </ContextMenu.Portal>
          </ContextMenu.Sub>
        ) : null}
      </ContextMenu.Content>
    </ContextMenu.Portal>
  )
}

function RowMenuItem({ children, icon, shortcut, disabled = false, onSelect }: { children: ReactNode; icon?: ReactNode; shortcut?: string; disabled?: boolean; onSelect: () => void }) {
  return (
    <ContextMenu.Item className="flow-inbox-menu__item" disabled={disabled} onSelect={onSelect}>
      {icon ? <span className="flow-inbox-menu__item-icon">{icon}</span> : null}
      <span className="flow-inbox-menu__item-label">{children}</span>
      {shortcut ? <kbd className="flow-inbox-menu__shortcut">{shortcut}</kbd> : null}
    </ContextMenu.Item>
  )
}

function RowSnoozeMenu({ notification, disabled, onSnooze }: { notification: InboxNotificationRowData; disabled: boolean; onSnooze: InboxNotificationRowProps['onSnooze'] }) {
  const [custom, setCustom] = useState(false)
  if (custom) {
    return <InboxCustomSnoozeForm
      disabled={disabled}
      onCancel={() => setCustom(false)}
      onConfirm={(preset) => onSnooze(notification, preset)}
    />
  }
  return <>
    <div className="flow-inbox-menu__search-wrap flow-inbox-menu__search-wrap--passive">
      <span>Try: 4 pm, 2 days, in 5 weeks…</span>
    </div>
    <div className="flow-inbox-menu__separator" />
    <SnoozeItem label="An hour from now" preset="hour" disabled={disabled} onSelect={onSnooze} notification={notification} />
    <SnoozeItem label="Tomorrow" preset="tomorrow" disabled={disabled} onSelect={onSnooze} notification={notification} />
    <SnoozeItem label="Next week" preset="nextWeek" disabled={disabled} onSelect={onSnooze} notification={notification} />
    <SnoozeItem label="A month from now" preset="month" disabled={disabled} onSelect={onSnooze} notification={notification} />
    <ContextMenu.Item
      className="flow-inbox-menu__item"
      disabled={disabled}
      onSelect={(event) => {
        event.preventDefault()
        setCustom(true)
      }}
    >
      <span className="flow-inbox-menu__item-label">Custom…</span>
    </ContextMenu.Item>
  </>
}

function SnoozeItem({ label, preset, notification, disabled, onSelect }: { label: string; preset: Exclude<InboxSnoozePreset, { kind: 'custom' }>; notification: InboxNotificationRowData; disabled: boolean; onSelect: InboxNotificationRowProps['onSnooze'] }) {
  return <RowMenuItem disabled={disabled} onSelect={() => onSelect(notification, preset)}>{label}</RowMenuItem>
}

function KeyboardSnoozeMenu({ notification, disabled, onSnooze }: {
  notification: InboxNotificationRowData
  disabled: boolean
  onSnooze: (preset: InboxSnoozePreset) => void
}) {
  const [custom, setCustom] = useState(false)
  const presets: Array<[Exclude<InboxSnoozePreset, { kind: 'custom' }>, string]> = [
    ['hour', 'An hour from now'],
    ['tomorrow', 'Tomorrow'],
    ['nextWeek', 'Next week'],
    ['month', 'A month from now'],
  ]

  if (custom) {
    return <InboxCustomSnoozeForm
      disabled={disabled}
      onCancel={() => setCustom(false)}
      onConfirm={onSnooze}
    />
  }

  return <div role="menu" aria-label="Snooze notification" onKeyDown={moveSnoozeFocus}>
    <div className="flow-inbox-menu__search-wrap flow-inbox-menu__search-wrap--passive">
      <span>Try: 4 pm, 2 days, in 5 weeks…</span>
    </div>
    <div className="flow-inbox-menu__separator" />
    {presets.map(([preset, label]) => (
      <button
        key={preset}
        type="button"
        role="menuitem"
        className={`flow-inbox-menu__item ${styles.snoozeOption}`}
        data-snooze-option={notification.id}
        disabled={disabled}
        onClick={() => onSnooze(preset)}
      >
        <span className="flow-inbox-menu__item-label">{label}</span>
      </button>
    ))}
    <button
      type="button"
      role="menuitem"
      className={`flow-inbox-menu__item ${styles.snoozeOption}`}
      data-snooze-option={notification.id}
      disabled={disabled}
      onClick={() => setCustom(true)}
    >
      <span className="flow-inbox-menu__item-label">Custom…</span>
    </button>
  </div>
}

function moveSnoozeFocus(event: KeyboardEvent<HTMLDivElement>) {
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp' && event.key !== 'Home' && event.key !== 'End') return
  const options = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[data-snooze-option]'))
  const current = document.activeElement
  const index = options.findIndex(option => option === current)
  if (index < 0 || !options.length) return
  event.preventDefault()
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? options.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length
  options[next]?.focus()
}

export function InboxCustomSnoozeForm({ disabled = false, onCancel, onConfirm }: { disabled?: boolean; onCancel: () => void; onConfirm: (preset: Extract<InboxSnoozePreset, { kind: 'custom' }>) => void }) {
  const [value, setValue] = useState(() => localDateTimeValue(defaultCustomSnoozeDate()))
  const [invalid, setInvalid] = useState(false)

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const selected = new Date(value)
    if (!value || Number.isNaN(selected.getTime())) {
      setInvalid(true)
      return
    }
    onConfirm({ kind: 'custom', snoozedUntil: selected.toISOString() })
  }

  return <form className="flow-inbox-snooze-custom" aria-label="Custom snooze date and time" onKeyDown={event => { if (event.key === 'Escape') { event.preventDefault(); onCancel() } }} onSubmit={submit}>
    <label className="flow-inbox-snooze-custom__field">
      <span>Date and time</span>
      <DateTimeControl label="Snooze until" min={localDateTimeValue(new Date())} mode="datetime" value={value} onChange={next => { setValue(next); setInvalid(false) }}/>
    </label>
    {invalid ? <p className="flow-inbox-snooze-custom__error" role="alert">Choose a valid date and time.</p> : null}
    <div className="flow-inbox-snooze-custom__actions">
      <button type="button" disabled={disabled} onClick={onCancel}>Back</button>
      <button type="submit" disabled={disabled || !value}>Snooze</button>
    </div>
  </form>
}

function defaultCustomSnoozeDate() {
  const next = new Date()
  next.setMinutes(0, 0, 0)
  next.setHours(9, 0, 0, 0)
  next.setDate(next.getDate() + 1)
  return next
}

function localDateTimeValue(date: Date) {
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16)
}

function isPromise(value: unknown): value is Promise<void> {
  return typeof value === 'object' && value !== null && 'then' in value && typeof value.then === 'function'
}

function cssAttributeValue(value: string) {
  return value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')
}

function initials(value: string) {
  return value.split(/\s|@/).filter(Boolean).slice(0, 2).map((part) => part[0]?.toUpperCase()).join('') || '?'
}

function actorColor(value: string) {
  const colors = ['lch(40% 60 0)', 'lch(55% 60 40)', 'lch(62% 45 145)', 'lch(70% 60 220)', 'lch(55% 55 285)', 'lch(60% 55 335)']
  const index = [...value].reduce((sum, character) => sum + character.charCodeAt(0), 0) % colors.length
  return colors[index]
}

function KindIcon({ kind }: { kind: InboxNotificationKind }) {
  if (kind === 'comment') return <svg viewBox="0 0 16 16"><path d="M6.27246 4.61328C6.34825 4.59068 6.4363 4.6001 6.5127 4.63477C6.58913 4.66988 6.64925 4.72787 6.68164 4.7998C6.71377 4.8718 6.716 4.95477 6.69141 5.03516C6.66653 5.1151 6.61688 5.18691 6.5498 5.22852C6.49137 5.26498 6.43537 5.30181 6.38086 5.33984C5.63535 5.83563 5.24006 6.55883 5.17773 7.29395C5.29094 7.27427 5.40754 7.26274 5.52637 7.2627C6.64457 7.2627 7.55148 8.16897 7.55176 9.28711C7.55176 10.4055 6.64474 11.3125 5.52637 11.3125C4.40834 11.3121 3.50195 10.4052 3.50195 9.28711V8.4873L3.50098 8.48828C3.499 8.39852 3.49934 8.30601 3.50293 8.2168C3.51156 6.5902 4.7032 5.09714 6.05078 4.6875C6.12397 4.66097 6.19724 4.6368 6.27246 4.61328ZM11.2725 4.61328C11.3482 4.59068 11.4363 4.6001 11.5127 4.63477C11.5891 4.66988 11.6492 4.72787 11.6816 4.7998C11.7138 4.8718 11.716 4.95477 11.6914 5.03516C11.6665 5.1151 11.6169 5.18691 11.5498 5.22852C11.4914 5.26498 11.4354 5.30181 11.3809 5.33984C10.6353 5.83563 10.2401 6.55883 10.1777 7.29395C10.2909 7.27427 10.4075 7.26274 10.5264 7.2627C11.6446 7.2627 12.5515 8.16897 12.5518 9.28711C12.5518 10.4055 11.6447 11.3125 10.5264 11.3125C9.40834 11.3121 8.50195 10.4052 8.50195 9.28711V8.4873L8.50098 8.48828C8.499 8.39852 8.49934 8.30601 8.50293 8.2168C8.51156 6.5902 9.7032 5.09714 11.0508 4.6875C11.124 4.66097 11.1972 4.6368 11.2725 4.61328Z" /></svg>
  if (kind === 'assignment') return <svg viewBox="0 0 16 16"><path d="M10.0252 4.76263C10.3259 4.51725 10.7602 4.54391 11.0291 4.81243L11.0808 4.86907L13.2478 7.52435C13.4727 7.80025 13.4728 8.19669 13.2478 8.47259L11.0808 11.1288C10.8192 11.4496 10.3471 11.4976 10.0261 11.2363C9.70516 10.9745 9.65691 10.5016 9.91872 10.1806L11.0838 8.75188H8.00173C7.18183 8.75183 6.17912 8.6165 5.28981 8.17083C4.43401 7.74179 3.68415 7.02179 3.33864 5.9052L3.27517 5.67669L3.26052 5.60149C3.20657 5.22513 3.44514 4.86208 3.82204 4.76849C4.19896 4.67511 4.58054 4.88422 4.70876 5.24212L4.73024 5.31536L4.76931 5.45403C4.97617 6.12866 5.41212 6.55408 5.96267 6.83001C6.57335 7.13596 7.32185 7.25183 8.00173 7.25188H11.0886L9.91872 5.81829L9.87282 5.75579C9.66379 5.43863 9.72463 5.0081 10.0252 4.76263Z" /></svg>
  if (kind === 'mention') return <svg viewBox="0 0 16 16"><path d="M8 2a6 6 0 1 0 3.7 10.72.75.75 0 1 0-.93-1.18A4.5 4.5 0 1 1 12.5 8v.75a.75.75 0 0 1-1.5 0V8a3 3 0 1 0-1.03 2.27A2.25 2.25 0 0 0 14 8 6 6 0 0 0 8 2Zm0 4.5A1.5 1.5 0 1 1 8 9.5a1.5 1.5 0 0 1 0-3Z" /></svg>
  return <svg viewBox="0 0 16 16"><path d="M8 2.25a.75.75 0 0 1 .75.75v4.69l2.78 1.6a.75.75 0 1 1-.75 1.3l-3.15-1.82A.75.75 0 0 1 7.25 8V3A.75.75 0 0 1 8 2.25Z" /></svg>
}

function UnreadIcon() { return <svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="5.75" fill="none" stroke="currentColor" strokeWidth="1.5" /></svg> }
function DeleteIcon() { return <svg viewBox="0 0 16 16"><path d="M3 4.5h10M6 2.5h4m-6 2 .6 9h6.8l.6-9M6.5 7v4m3-4v4" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" /></svg> }
function SnoozeIcon() { return <svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.3" /><path d="M8 4.8v3.4l2.2 1.3" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" /></svg> }
function FavoriteIcon() { return <svg viewBox="0 0 16 16"><path d="m8 2 1.8 3.6 4 .6-2.9 2.8.7 4L8 11.1 4.4 13l.7-4-2.9-2.8 4-.6L8 2Z" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round" /></svg> }
function CopyIcon() { return <svg viewBox="0 0 16 16"><rect x="5.5" y="5.5" width="7" height="7" rx="1" fill="none" stroke="currentColor" strokeWidth="1.3" /><path d="M10.5 5.5v-2a1 1 0 0 0-1-1h-6a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2" fill="none" stroke="currentColor" strokeWidth="1.3" /></svg> }
