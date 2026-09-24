/**
 * LS-0265 FeedItemCommon — shared byline / content / title primitives for Pulse cards.
 */
import { formatDistanceToNowStrict, format } from 'date-fns'
import type { ReactNode } from 'react'
import { Avatar } from '@/components/issue/issue-row'
import type { User } from '@/types/flow'
import './feed-item-common.css'

export type FeedItemUpdateType =
  | 'summary'
  | 'update'
  | 'projectUpdate'
  | 'initiativeUpdate'
  | 'teamUpdate'

export function feedItemSharedLabel(type: FeedItemUpdateType, teamName?: string): string {
  switch (type) {
    case 'summary':
      return 'generated'
    case 'update':
      return teamName ? `an update in ${teamName}` : 'an update'
    case 'projectUpdate':
      return 'a project update'
    case 'initiativeUpdate':
      return 'an initiative update'
    case 'teamUpdate':
      return 'a team update'
  }
}

export type FeedItemBylineProps = {
  author?: User
  type: FeedItemUpdateType
  date: string | Date
  teamName?: string
  hideShared?: boolean
  trailing?: ReactNode
  className?: string
}

export function FeedItemByline({
  author,
  type,
  date,
  teamName,
  hideShared = false,
  trailing,
  className,
}: FeedItemBylineProps) {
  const when = date instanceof Date ? date : new Date(date)
  const relative = formatDistanceToNowStrict(when, { addSuffix: true })
  const absolute = format(when, 'EEEE, MMM d, yyyy · HH:mm')

  if (type === 'summary') {
    return (
      <div className={['feed-item-byline', className].filter(Boolean).join(' ')} data-surface="LS-0265">
        <span className="feed-item-byline__generated">Generated</span>
        <time className="feed-item-byline__date" title={absolute}>{relative}</time>
        {trailing}
      </div>
    )
  }

  const name = author ? author.displayName || author.name : undefined

  return (
    <div className={['feed-item-byline', className].filter(Boolean).join(' ')} data-surface="LS-0265">
      {author && <Avatar name={name || 'User'} />}
      {name && (
        <span className="feed-item-byline__text">
          <strong className="feed-item-user-name">{name}</strong>
          {!hideShared && (
            <span className="feed-item-byline__shared">{` shared ${feedItemSharedLabel(type, teamName)}`}</span>
          )}
        </span>
      )}
      <time className="feed-item-byline__date" title={absolute}>{relative}</time>
      {trailing}
    </div>
  )
}

export function FeedItemContent({
  children,
  className,
}: {
  children: ReactNode
  className?: string
}) {
  return (
    <div className={['feed-item-content', className].filter(Boolean).join(' ')} data-feed-item-content="">
      {children}
    </div>
  )
}

export function FeedItemTitle({
  children,
  className,
  as: Tag = 'h3',
}: {
  children: ReactNode
  className?: string
  as?: 'h2' | 'h3' | 'h4' | 'div' | 'span'
}) {
  return (
    <Tag className={['feed-item-title', className].filter(Boolean).join(' ')} data-feed-item-title="">
      {children}
    </Tag>
  )
}

export function FeedItemHeader({
  title,
  byline,
  menu,
  className,
}: {
  title?: ReactNode
  byline?: ReactNode
  menu?: ReactNode
  className?: string
}) {
  return (
    <div className={['feed-item-byline-row', className].filter(Boolean).join(' ')} data-feed-item-header="">
      <div className="feed-item-byline-row__main">
        {title}
        {byline}
      </div>
      {menu}
    </div>
  )
}
