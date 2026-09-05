import type { ReactNode } from 'react'

export function InboxListLoading({ count = 7 }: { count?: number }) {
  return (
    <div className="flow-inbox-list-state flow-inbox-list-state--loading" role="status" aria-live="polite" aria-label="Loading notifications">
      {Array.from({ length: count }, (_, index) => <InboxRowSkeleton key={index} />)}
    </div>
  )
}

export function InboxListEmpty({ title = 'All caught up', description = 'No notifications to show.', onShowAll }: { title?: string; description?: string; onShowAll?: () => void }) {
  return (
    <div className="flow-inbox-list-state flow-inbox-list-state--message" role="status" aria-live="polite">
      <EmptyInboxIcon />
      <strong>{title}</strong>
      <span>{description}</span>
      {onShowAll && <button type="button" onClick={onShowAll}>Show all notifications</button>}
    </div>
  )
}

export function InboxListError({ onRetry, title = 'Unable to load notifications' }: { onRetry: () => void; title?: string }) {
  return (
    <div className="flow-inbox-list-state flow-inbox-list-state--message" role="alert">
      <ErrorIcon />
      <strong>{title}</strong>
      <button type="button" onClick={onRetry}>Try again</button>
    </div>
  )
}

export function InboxListBoundary({ loading, error, empty, retry, onShowAll, children }: { loading: boolean; error?: boolean; empty?: boolean; retry: () => void; onShowAll?: () => void; children: ReactNode }) {
  if (loading) return <InboxListLoading />
  if (error) return <InboxListError onRetry={retry} />
  if (empty) return <InboxListEmpty onShowAll={onShowAll} />
  return <>{children}</>
}

function InboxRowSkeleton() {
  return <div className="flow-inbox-row-skeleton"><i /><span><b /><em /></span></div>
}

function EmptyInboxIcon() { return <svg viewBox="0 0 48 48" aria-hidden="true"><path d="M10 13h28l5 20H29l-2 3h-6l-2-3H5l5-20Z" /><path d="M10 13 14 7h20l4 6" /></svg> }
function ErrorIcon() { return <svg viewBox="0 0 48 48" aria-hidden="true"><circle cx="24" cy="24" r="18" /><path d="M24 14v12m0 7h.01" /></svg> }
