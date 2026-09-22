/**
 * LS-0296 InboxActionControls — unified subscribe / triage / snooze / delete
 * toolbar pack for inbox detail hosts.
 */
import type { ReactNode } from 'react'
import { Bell, BellOff, Clock, MoreHorizontal, Star, Trash2 } from 'lucide-react'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'

import type { InboxSnoozePreset } from '../notification-row'

import './inbox-hosts.css'

export type InboxActionControlsProps = {
  favorite?: boolean
  subscribed?: boolean
  pending?: boolean
  showSubscribe?: boolean
  showTriage?: boolean
  triageActions?: Array<{ id: string; label: string; onSelect: () => void }>
  onFavoriteChange?: (favorite: boolean) => void
  onSubscribeChange?: (subscribed: boolean) => void
  onSnooze?: (preset: InboxSnoozePreset) => void
  onDelete?: () => void
  extra?: ReactNode
}

const SNOOZE_ITEMS: Array<[Exclude<InboxSnoozePreset, { kind: 'custom' }>, string]> = [
  ['hour', 'An hour from now'],
  ['tomorrow', 'Tomorrow'],
  ['nextWeek', 'Next week'],
  ['month', 'A month from now'],
]

export function InboxActionControls({
  favorite = false,
  subscribed = false,
  pending = false,
  showSubscribe = false,
  showTriage = false,
  triageActions = [],
  onFavoriteChange,
  onSubscribeChange,
  onSnooze,
  onDelete,
  extra,
}: InboxActionControlsProps) {
  return (
    <div className="flow-inbox-action-controls" data-surface="LS-0296" role="toolbar" aria-label="Inbox actions">
      {onFavoriteChange ? (
        <ActionButton
          disabled={pending}
          label={favorite ? 'Remove from favorites' : 'Add to favorites'}
          pressed={favorite}
          onClick={() => onFavoriteChange(!favorite)}
        >
          <Star size={14} fill={favorite ? 'currentColor' : 'none'} />
        </ActionButton>
      ) : null}

      {showSubscribe && onSubscribeChange ? (
        <ActionButton
          disabled={pending}
          label={subscribed ? 'Unsubscribe' : 'Subscribe'}
          pressed={subscribed}
          onClick={() => onSubscribeChange(!subscribed)}
        >
          {subscribed ? <BellOff size={14} /> : <Bell size={14} />}
        </ActionButton>
      ) : null}

      {onSnooze ? (
        <DropdownMenu.Root modal={false}>
          <DropdownMenu.Trigger asChild>
            <ActionButton disabled={pending} label="Snooze notification">
              <Clock size={14} />
            </ActionButton>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content
              align="end"
              className="flow-inbox-menu flow-inbox-action-controls__menu"
              data-flow-motion="floating"
              sideOffset={4}
            >
              <DropdownMenu.Label className="flow-inbox-action-controls__menu-label">Snooze</DropdownMenu.Label>
              {SNOOZE_ITEMS.map(([preset, label]) => (
                <DropdownMenu.Item
                  className="flow-inbox-action-controls__menu-item"
                  key={preset}
                  onSelect={() => onSnooze(preset)}
                >
                  {label}
                </DropdownMenu.Item>
              ))}
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      ) : null}

      {showTriage && triageActions.length ? (
        <DropdownMenu.Root modal={false}>
          <DropdownMenu.Trigger asChild>
            <ActionButton disabled={pending} label="Triage actions">
              <MoreHorizontal size={14} />
            </ActionButton>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content
              align="end"
              className="flow-inbox-menu flow-inbox-action-controls__menu"
              data-flow-motion="floating"
              sideOffset={4}
            >
              <DropdownMenu.Label className="flow-inbox-action-controls__menu-label">
                Triage actions
              </DropdownMenu.Label>
              {triageActions.map(action => (
                <DropdownMenu.Item
                  className="flow-inbox-action-controls__menu-item"
                  key={action.id}
                  onSelect={action.onSelect}
                >
                  {action.label}
                </DropdownMenu.Item>
              ))}
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      ) : null}

      {onDelete ? (
        <ActionButton disabled={pending} label="Delete notification" onClick={onDelete}>
          <Trash2 size={14} />
        </ActionButton>
      ) : null}

      {extra}
    </div>
  )
}

function ActionButton({
  children,
  label,
  disabled,
  pressed,
  onClick,
}: {
  children: ReactNode
  label: string
  disabled?: boolean
  pressed?: boolean
  onClick?: () => void
}) {
  return (
    <button
      aria-label={label}
      aria-pressed={pressed}
      className="flow-inbox-action-controls__button"
      disabled={disabled}
      onClick={onClick}
      title={label}
      type="button"
    >
      {children}
    </button>
  )
}
