import type { ReactNode } from 'react'
import { useRef, useState } from 'react'
import type { Issue, IssueRelationType, IssueUpdateInput } from '@/types/flow'

import { InboxDetailError, InboxDetailLoading, InboxDetailPreview } from './inbox-detail-preview'
import { InboxListBoundary } from './inbox-list-state'
import { InboxNotificationList } from './notification-list'
import { InboxPageShell, type InboxDisplayOptions } from './inbox-page-shell'
import { useInboxController, type InboxPersistenceAdapter } from './inbox-controller'
import type { InboxFilterCondition, InboxFilterOptions } from './inbox-filter-builder'
import type { InboxNotificationRowData } from './notification-row'

export interface InboxPageAdapter extends InboxPersistenceAdapter {
  deleteAll: () => Promise<void>
  deleteAllRead: () => Promise<void>
  deleteAllReadCompleted: () => Promise<void>
}

export interface InboxDetailRenderResult {
  content: ReactNode
  fullBleed?: boolean
  loading?: boolean
  error?: boolean
  retry?: () => void
  issue?: Issue
  issues?: Issue[]
  onUpdateIssue?: (input: IssueUpdateInput) => Promise<void>
  onDeleteIssue?: () => Promise<void>
  onCreateRelation?: (type: IssueRelationType, relatedIssueId: string) => Promise<void>
}

export interface InboxPageProps {
  notifications: InboxNotificationRowData[]
  selectedId?: string | null
  onNotificationsChange: (notifications: InboxNotificationRowData[]) => void
  onSelectedIdChange: (id: string | null) => void
  adapter: InboxPageAdapter
  displayOptions: InboxDisplayOptions
  onDisplayOptionsChange: (options: InboxDisplayOptions) => void
  filters?: InboxFilterCondition[]
  filterOptions?: InboxFilterOptions
  filterHiddenCount?: number
  onFiltersChange?: (filters: InboxFilterCondition[]) => void
  renderDetail: (notification: InboxNotificationRowData) => InboxDetailRenderResult
  onOpenIssue: (notification: InboxNotificationRowData) => void
  onOpenSidebar?: () => void
  onCopyLink?: (notification: InboxNotificationRowData) => void
  onCopyIdentifier?: (notification: InboxNotificationRowData) => void
  subscribed?: (notification: InboxNotificationRowData) => boolean
  onSubscribeChange?: (notification: InboxNotificationRowData, subscribed: boolean) => void
  loading?: boolean
  loadError?: boolean
  onRetryLoad: () => void
  hasMore?: boolean
  loadingMore?: boolean
  onLoadMore?: () => void
}

export function InboxPage(props: InboxPageProps) {
  const [pageError, setPageError] = useState<string | null>(null)
  const [bulkPending, setBulkPending] = useState<string | null>(null)
  const compositionRef = useRef<HTMLDivElement>(null)
  const controller = useInboxController({
    notifications: props.notifications,
    selectedId: props.selectedId,
    onNotificationsChange: props.onNotificationsChange,
    onSelectedIdChange: props.onSelectedIdChange,
    persistence: props.adapter,
  })
  const selected = props.notifications.find(notification => notification.id === props.selectedId)
  const detail = selected ? props.renderDetail(selected) : null
  const mutationError = controller.error?.message ?? pageError
  const closeDetail = () => {
    const id = selected?.id
    controller.actions.close()
    window.requestAnimationFrame(() => {
      if (!id) return
      compositionRef.current?.querySelector<HTMLElement>(`[data-notification-id="${escapeSelector(id)}"] [role="link"]`)?.focus()
    })
  }

  const runPageAction = async (action: string, work: () => Promise<void>) => {
    if (bulkPending) return
    setPageError(null)
    setBulkPending(action)
    try {
      await work()
    } catch (cause) {
      setPageError(cause instanceof Error ? cause.message : 'The Inbox action failed.')
    } finally {
      setBulkPending(null)
    }
  }

  const preview = selected && detail ? (
    <InboxDetailPreview
      notification={selected}
      pending={Boolean(controller.pending[selected.id])}
      issue={detail.issue}
      issues={detail.issues}
      fullBleed={detail.fullBleed}
      subscribed={props.subscribed?.(selected)}
      onBack={closeDetail}
      onOpenIssue={props.onOpenIssue}
      onFavoriteChange={(notification, favorite) => void controller.actions.setFavorite(notification, favorite).catch(() => undefined)}
      onReadChange={(notification, read) => void controller.actions.setRead(notification, read).catch(() => undefined)}
      onSubscribeChange={props.onSubscribeChange}
      onSnooze={(notification, preset) => void controller.actions.snooze(notification, preset).catch(() => undefined)}
      onDelete={notification => void controller.actions.delete(notification).catch(() => undefined)}
      onUpdateIssue={detail.onUpdateIssue}
      onDeleteIssue={detail.onDeleteIssue}
      onCreateRelation={detail.onCreateRelation}
      onCopyLink={props.onCopyLink}
      onCopyIdentifier={props.onCopyIdentifier}
    >
      {detail.loading ? <InboxDetailLoading /> : detail.error ? <InboxDetailError onRetry={detail.retry ?? props.onRetryLoad} /> : detail.content}
    </InboxDetailPreview>
  ) : undefined

  return (
    <div className="flow-inbox-page-composition" ref={compositionRef}>
      <InboxPageShell
        detail={preview}
        displayOptions={props.displayOptions}
        filters={props.filters}
        filterOptions={props.filterOptions}
        filterHiddenCount={props.filterHiddenCount}
        bulkPending={Boolean(bulkPending)}
        onFiltersChange={props.onFiltersChange}
        onDisplayOptionsChange={props.onDisplayOptionsChange}
        onOpenSidebar={props.onOpenSidebar}
        onDeleteAll={() => void runPageAction('all', props.adapter.deleteAll)}
        onDeleteAllRead={() => void runPageAction('read', props.adapter.deleteAllRead)}
        onDeleteAllReadCompleted={() => void runPageAction('completed', props.adapter.deleteAllReadCompleted)}
      >
        <InboxListBoundary loading={Boolean(props.loading)} error={props.loadError} empty={!props.notifications.length} retry={props.onRetryLoad}>
          <InboxNotificationList
            notifications={props.notifications}
            selectedId={props.selectedId}
            pending={controller.pending}
            hasMore={props.hasMore}
            loadingMore={props.loadingMore}
            onLoadMore={props.onLoadMore}
            onOpen={controller.actions.open}
            onReadChange={(notification, read) => void controller.actions.setRead(notification, read).catch(() => undefined)}
            onDelete={notification => void controller.actions.delete(notification).catch(() => undefined)}
            onSnooze={(notification, preset) => void controller.actions.snooze(notification, preset).catch(() => undefined)}
            onFavoriteChange={(notification, favorite) => void controller.actions.setFavorite(notification, favorite).catch(() => undefined)}
            onCopyLink={props.onCopyLink}
            onCopyIdentifier={props.onCopyIdentifier}
          />
        </InboxListBoundary>
      </InboxPageShell>
      {mutationError ? (
        <div className="flow-inbox-mutation-error" role="alert">
          <span>{mutationError}</span>
          {controller.error ? <button type="button" onClick={() => void controller.retry()}>Retry</button> : null}
          <button type="button" aria-label="Dismiss error" onClick={() => { controller.dismissError(); setPageError(null) }}>×</button>
        </div>
      ) : null}
    </div>
  )
}

function escapeSelector(value: string) {
  return globalThis.CSS?.escape ? globalThis.CSS.escape(value) : value.replace(/["\\]/g, '\\$&')
}
