import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Dialog from '@radix-ui/react-dialog'
import type { ButtonHTMLAttributes, ReactNode } from 'react'
import { forwardRef, useEffect, useState } from 'react'

import { IssueOptionsMenu } from '@/components/issue/issue-options-menu'
import { RelationPicker } from '@/components/issue/relation-picker'
import type { Issue, IssueRelationType, IssueUpdateInput } from '@/types/flow'

import { InboxCustomSnoozeForm, type InboxNotificationRowData, type InboxSnoozePreset } from './notification-row'
import './inbox-detail-preview.css'

export interface InboxDetailPreviewProps {
  notification: InboxNotificationRowData
  issue?: Issue
  issues?: Issue[]
  children: ReactNode
  fullBleed?: boolean
  subscribed?: boolean
  pending?: boolean
  onBack: () => void
  onOpenIssue: (notification: InboxNotificationRowData) => void
  onFavoriteChange: (notification: InboxNotificationRowData, favorite: boolean) => void
  onReadChange: (notification: InboxNotificationRowData, read: boolean) => void
  onSubscribeChange?: (notification: InboxNotificationRowData, subscribed: boolean) => Promise<void> | void
  onSnooze: (notification: InboxNotificationRowData, preset: InboxSnoozePreset) => void
  onDelete: (notification: InboxNotificationRowData) => void
  onUpdateIssue?: (input: IssueUpdateInput) => Promise<void>
  onDeleteIssue?: () => Promise<void>
  onCreateRelation?: (type: IssueRelationType, relatedIssueId: string) => Promise<void>
  onCopyLink?: (notification: InboxNotificationRowData) => void
  onCopyIdentifier?: (notification: InboxNotificationRowData) => void
}

export function InboxDetailPreview(props: InboxDetailPreviewProps) {
  const { notification } = props
  const identifier = notification.identifier?.trim() || 'Issue'
  const title = notification.title?.trim() || 'Untitled issue'
  const [relationType, setRelationType] = useState<IssueRelationType | null>(null)
  const [snoozeOpen, setSnoozeOpen] = useState(false)
  const [historyOpen, setHistoryOpen] = useState(false)

  useEffect(() => {
    setRelationType(null)
    setSnoozeOpen(false)
    setHistoryOpen(false)
  }, [notification.id])

  const issueOptions = props.issue && props.onUpdateIssue && props.onDeleteIssue && props.onCreateRelation ? (
    <IssueOptionsMenu
      issue={props.issue}
      issueUrl={props.onCopyLink ? undefined : window.location.href}
      onUpdate={props.onUpdateIssue}
      onDelete={props.onDeleteIssue}
      onRelation={setRelationType}
      onFavoriteChange={() => props.onFavoriteChange(notification, !notification.favorite)}
      onRemind={() => setSnoozeOpen(true)}
      onShowDescriptionHistory={() => setHistoryOpen(true)}
      trigger={<DetailIconButton data-detail-action="issue-options" disabled={props.pending} label="Issue options"><MoreDetailIcon /></DetailIconButton>}
    />
  ) : <DetailOptionsFallback {...props} />

  return (
    <article className="flow-inbox-detail" aria-label={`${identifier} ${title}`} aria-busy={props.pending || undefined}>
      <header className="flow-inbox-detail__header">
        <button className="flow-inbox-detail__back" type="button" aria-label="Back to Inbox" onClick={props.onBack}>
          <BackIcon />
          <span>Inbox</span><i>›</i>
        </button>
        <button className="flow-inbox-detail__source" type="button" aria-label={`Open ${identifier}: ${title}`} onClick={() => props.onOpenIssue(notification)} title={title}>
          <b>{identifier}</b><span>{title}</span>
        </button>
        <div className="flow-inbox-detail__actions">
          <DetailIconButton data-detail-action="favorite" disabled={props.pending} label={notification.favorite ? 'Remove from favorites' : 'Add to favorites'} pressed={notification.favorite} onClick={() => props.onFavoriteChange(notification, !notification.favorite)}><StarIcon /></DetailIconButton>
          {issueOptions}
          {props.onSubscribeChange ? <DetailIconButton data-detail-action="subscription" disabled={props.pending} label={props.subscribed ? 'Unsubscribe from issue' : 'Subscribe to issue'} pressed={props.subscribed} onClick={() => props.onSubscribeChange?.(notification, !props.subscribed)}><BellIcon /></DetailIconButton> : null}
          <DetailSnoozeMenu notification={notification} onSnooze={props.onSnooze} pending={props.pending} open={snoozeOpen} onOpenChange={setSnoozeOpen} />
          <DetailIconButton data-detail-action="delete" disabled={props.pending} label="Delete notification" onClick={() => props.onDelete(notification)}><DeleteDetailIcon /></DetailIconButton>
        </div>
      </header>
      <div className={`flow-inbox-detail__body ${props.fullBleed ? 'flow-inbox-detail__body--full-bleed' : ''}`}>
        {props.fullBleed ? props.children : <div className="flow-inbox-detail__scroll">{props.children}</div>}
        {props.pending ? <div className="flow-inbox-detail__pending" role="status" aria-label="Updating notification"><i /></div> : null}
      </div>
      {relationType && props.issue && props.issues && props.onCreateRelation ? <RelationPicker open onOpenChange={open => { if (!open) setRelationType(null) }} type={relationType} issueId={props.issue.id} issues={props.issues} onSelect={relatedIssueId => props.onCreateRelation?.(relationType, relatedIssueId)}/>: null}
      <DescriptionHistoryDialog issue={props.issue} open={historyOpen} onOpenChange={setHistoryOpen}/>
    </article>
  )
}

function DetailOptionsFallback(props: InboxDetailPreviewProps) {
  const { notification } = props
  return <DropdownMenu.Root modal={false}><DropdownMenu.Trigger asChild><DetailIconButton data-detail-action="issue-options" disabled={props.pending} label="Issue options"><MoreDetailIcon /></DetailIconButton></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-inbox-menu flow-inbox-detail-menu" align="end" sideOffset={4}>
    <DetailMenuItem shortcut="U" onSelect={() => props.onReadChange(notification, !notification.read)}>{notification.read ? 'Mark as unread' : 'Mark as read'}</DetailMenuItem>
    {props.onCopyLink ? <DetailMenuItem onSelect={() => props.onCopyLink?.(notification)}>Copy issue URL</DetailMenuItem> : null}
    {props.onCopyIdentifier ? <DetailMenuItem onSelect={() => props.onCopyIdentifier?.(notification)}>Copy issue ID</DetailMenuItem> : null}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function DetailSnoozeMenu({ notification, onSnooze, pending, open, onOpenChange }: Pick<InboxDetailPreviewProps, 'notification' | 'onSnooze' | 'pending'> & { open: boolean; onOpenChange: (open: boolean) => void }) {
  const [custom, setCustom] = useState(false)
  const items: Array<[Exclude<InboxSnoozePreset, { kind: 'custom' }>, string]> = [['hour','An hour from now'],['tomorrow','Tomorrow'],['nextWeek','Next week'],['month','A month from now']]
  const close = () => {
    onOpenChange(false)
    setCustom(false)
  }
  useEffect(() => {
    setCustom(false)
  }, [notification.id])
  return <DropdownMenu.Root modal={false} open={open} onOpenChange={(nextOpen) => { onOpenChange(nextOpen); if (!nextOpen) setCustom(false) }}><DropdownMenu.Trigger asChild><DetailIconButton data-detail-action="snooze" disabled={pending} label="Snooze notification"><ClockIcon /></DetailIconButton></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-inbox-menu flow-inbox-detail-snooze" align="end" sideOffset={4}>
    {custom ? <InboxCustomSnoozeForm disabled={pending} onCancel={() => setCustom(false)} onConfirm={(preset) => { onSnooze(notification, preset); close() }} /> : <>
      <div className="flow-inbox-menu__search-wrap flow-inbox-menu__search-wrap--passive"><span>Try: 4 pm, 2 days, in 5 weeks…</span></div>
      <div className="flow-inbox-menu__separator" />
      {items.map(([preset,label])=><DetailMenuItem key={preset} onSelect={()=>onSnooze(notification,preset)}>{label}</DetailMenuItem>)}
      <DetailMenuItem disabled={pending} onSelect={(event) => { event.preventDefault(); setCustom(true) }}>Custom…</DetailMenuItem>
    </>}
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function DescriptionHistoryDialog({ issue, open, onOpenChange }: { issue?: Issue; open: boolean; onOpenChange: (open: boolean) => void }) {
  if (!issue) return null
  return <Dialog.Root open={open} onOpenChange={onOpenChange}>
    <Dialog.Portal>
      <Dialog.Overlay className="dialog-overlay"/>
      <Dialog.Content className="inbox-description-history" aria-label="Description history">
        <Dialog.Title>Description history</Dialog.Title>
        <p className="inbox-description-history__timestamp">Current version · {new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' }).format(new Date(issue.updatedAt))}</p>
        <article>{issue.description || 'No description'}</article>
        <footer><button type="button" onClick={() => onOpenChange(false)}>Done</button></footer>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}

function DetailMenuItem({ children, shortcut, disabled = false, onSelect }: { children: ReactNode; shortcut?: string; disabled?: boolean; onSelect: (event: Event) => void }) {
  return <DropdownMenu.Item className="flow-inbox-menu__item" disabled={disabled} onSelect={onSelect}><span className="flow-inbox-menu__item-label">{children}</span>{shortcut ? <kbd className="flow-inbox-menu__shortcut">{shortcut}</kbd> : null}</DropdownMenu.Item>
}

interface DetailIconButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  label: string
  pressed?: boolean
  children: ReactNode
}

const DetailIconButton = forwardRef<HTMLButtonElement, DetailIconButtonProps>(function DetailIconButton({ label, pressed, children, ...buttonProps }, ref) {
  return <button {...buttonProps} ref={ref} className="flow-inbox-detail__icon-button" type="button" aria-label={label} aria-pressed={pressed} title={label}>{children}</button>
})

export function InboxDetailActivity({ actor, actorAvatarUrl, time, children }: { actor?: string; actorAvatarUrl?: string; time?: string; children: ReactNode }) {
  const actorName = actor?.trim() || 'Unknown user'
  const timeLabel = time?.trim() || 'Just now'
  return <section className="flow-inbox-detail-activity" aria-label="Notification activity"><h3>Activity</h3><div className="flow-inbox-detail-activity__meta">{actorAvatarUrl ? <img src={actorAvatarUrl} alt="" /> : <i>{actorName[0]?.toUpperCase() || '?'}</i>}<span><b>{actorName}</b> · {timeLabel}</span></div><div className="flow-inbox-detail-activity__content">{children}</div></section>
}

export function InboxDetailLoading() {
  return <div className="flow-inbox-detail-state" role="status" aria-label="Loading notification"><i /><b /><span /><span /><span /></div>
}

export function InboxDetailError({ onRetry }: { onRetry: () => void }) {
  return <div className="flow-inbox-detail-state flow-inbox-detail-state--error" role="alert"><strong>Unable to load notification</strong><button type="button" onClick={onRetry}>Try again</button></div>
}

function BackIcon(){return <svg viewBox="0 0 16 16"><path d="m10 3-5 5 5 5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>}
function StarIcon(){return <svg viewBox="0 0 16 16"><path d="m8 2 1.8 3.6 4 .6-2.9 2.8.7 4L8 11.1 4.4 13l.7-4-2.9-2.8 4-.6L8 2Z" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round" /></svg>}
function MoreDetailIcon(){return <svg viewBox="0 0 16 16"><path d="M3 6.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Z" /></svg>}
function BellIcon(){return <svg viewBox="0 0 16 16"><path d="M3.5 11h9l-1-1.7V6a3.5 3.5 0 1 0-7 0v3.3L3.5 11Zm3 2h3" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" /></svg>}
function ClockIcon(){return <svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.3" /><path d="M8 4.8v3.4l2.2 1.3" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" /></svg>}
function DeleteDetailIcon(){return <svg viewBox="0 0 16 16"><path d="M3 4.5h10M6 2.5h4m-6 2 .6 9h6.8l.6-9M6.5 7v4m3-4v4" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" /></svg>}
