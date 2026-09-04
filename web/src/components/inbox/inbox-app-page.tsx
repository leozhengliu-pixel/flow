import { useEffect, useMemo, useRef, useState } from 'react'

import type { ActivityEvent, BootstrapData, CodeReview, Issue, IssueRelationType, IssueUpdateInput, Notification, Presence, Project, User } from '@/types/flow'
import { DetailPane } from '@/components/detail/detail-pane'
import { NoProjectIcon, PriorityIcon, ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import type { SubIssueInput } from '@/components/issue/sub-issue-editor'
import { batchNotifications, updateInboxNotification } from '@/lib/api'

import { type InboxFilterCondition, type InboxFilterOptions } from './inbox-filter-builder'
import { INBOX_REVIEW_STATUS_OPTIONS, normalizeInboxFilters } from './inbox-filter-types'
import { InboxPage, type InboxPageAdapter } from './inbox-page'
import type { InboxDisplayOptions } from './inbox-page-shell'
import type { InboxNotificationKind, InboxNotificationRowData, InboxSnoozePreset } from './notification-row'

const initialDisplayOptions: InboxDisplayOptions = {
  ordering: 'newest',
  showSnoozed: false,
  showRead: true,
  showUnreadFirst: false,
}

interface InboxProjection extends InboxNotificationRowData {
  issueId: string
  sourceType: 'comment' | 'activity'
  sourceId: string
  notificationType: string
  actorId: string
  snoozedUntil?: string
  projectId?: string
  initiativeIds: string[]
  issuePriority: number
  issueStatusType: Issue['state']['type']
  reviewId?: string
  reviewStatus?: string
}

export interface InboxAppPageProps {
  data: BootstrapData
  presence?: Presence[]
  onReload: () => Promise<void> | void
  onOpenIssue: (issue: Issue) => void
  onOpenProject?: (project: Project) => void
  onOpenReview?: (review: CodeReview) => void
  onSubscriberChange?: (issue: Issue, subscribed: boolean) => Promise<void> | void
  onUpdateIssue?: (issue: Issue, input: IssueUpdateInput) => Promise<void>
  onDeleteIssue?: (issue: Issue) => Promise<void>
  onCreateRelation?: (issue: Issue, type: IssueRelationType, relatedIssueId: string) => Promise<void>
  onDeleteRelation: (issue: Issue, relationId: string) => Promise<void>
  onCreateSubIssue: (issue: Issue, input: SubIssueInput) => Promise<void>
  onReactIssue: (issue: Issue, emoji: string) => Promise<void>
  onCreateComment: (issue: Issue, body: string, bodyData?: Record<string, unknown>, parentId?: string) => Promise<void>
  onEditComment: (issue: Issue, commentId: string, body: string, bodyData?: Record<string, unknown>) => Promise<void>
  onDeleteComment: (issue: Issue, commentId: string) => Promise<void>
  onReactComment: (issue: Issue, commentId: string, emoji: string) => Promise<void>
  onUploadAttachment: (issue: Issue, file: File) => Promise<void>
  onDeleteAttachment: (issue: Issue, attachmentId: string) => Promise<void>
  onCopyIssueLink?: (issue: Issue) => Promise<void> | void
  onOpenSidebar?: () => void
}

export function InboxAppPage({ data, presence = [], onReload, onOpenIssue, onOpenProject, onOpenReview, onSubscriberChange, onUpdateIssue, onDeleteIssue, onCreateRelation, onDeleteRelation, onCreateSubIssue, onReactIssue, onCreateComment, onEditComment, onDeleteComment, onReactComment, onUploadAttachment, onDeleteAttachment, onCopyIssueLink, onOpenSidebar }: InboxAppPageProps) {
  const source = useMemo(() => projectInbox(data), [data])
  const issueById = useMemo(() => new Map(data.issues.map(issue => [issue.id, issue])), [data.issues])
  const [notifications, setNotifications] = useState<InboxProjection[]>(source)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [displayOptions, setDisplayOptions] = useState(readInboxDisplayOptions)
  const [filters, setFilters] = useState<InboxFilterCondition[]>(readInboxFilters)
  const filterOptions = useMemo<InboxFilterOptions>(
    () => buildInboxFilterOptions(notifications, filters, displayOptions, data),
    [data, displayOptions, filters, notifications],
  )
  const notificationsRef = useRef(notifications)
  notificationsRef.current = notifications
  const sourceByIdRef = useRef(new Map(source.map(notification => [notification.id, notification])))

  useEffect(() => {
    const url = new URL(window.location.href)
    if (filters.length) url.searchParams.set('filter', encodeInboxFilters(filters))
    else url.searchParams.delete('filter')
    window.history.replaceState(window.history.state, '', url)
  }, [filters])

  useEffect(() => {
    try {
      window.localStorage.setItem('flow.inbox.display-options', JSON.stringify(displayOptions))
    } catch {
      // Storage can be unavailable in private contexts; the in-memory value remains valid.
    }
  }, [displayOptions])

  useEffect(() => {
    sourceByIdRef.current = new Map(source.map(notification => [notification.id, notification]))
    setNotifications(source)
    setSelectedId(current => source.some(notification => notification.id === current) ? current : null)
  }, [source])

  const adapter = useMemo<InboxPageAdapter>(() => ({
    setRead: async (id, read) => {
      await updateInboxNotification(id, { read })
    },
    delete: async id => {
      await updateInboxNotification(id, { deleted: true })
    },
    snooze: async (id, preset) => {
      const snoozedUntil = resolveSnoozedUntil(preset)
      await updateInboxNotification(id, { snoozedUntil })
      setNotifications(current => {
        const existing = current.find(notification => notification.id === id) ?? sourceByIdRef.current.get(id)
        return existing ? putProjection(current, { ...existing, snoozedUntil }) : current
      })
    },
    setFavorite: async (id, favorite) => {
      await updateInboxNotification(id, { favorite })
    },
    deleteAll: async () => {
      const snapshot = notificationsRef.current
      setNotifications([])
      try { await batchNotifications('deleteAll') } catch (error) { setNotifications(snapshot); throw error }
    },
    deleteAllRead: async () => {
      const snapshot = notificationsRef.current
      setNotifications(current => current.filter(notification => !notification.read))
      try { await batchNotifications('deleteRead') } catch (error) { setNotifications(snapshot); throw error }
    },
    deleteAllReadCompleted: async () => {
      const completedIssueIds = new Set(data.issues.filter(issue => issue.state.type === 'completed').map(issue => issue.id))
      const snapshot = notificationsRef.current
      setNotifications(current => current.filter(notification => !(notification.read && completedIssueIds.has(notification.issueId))))
      try { await batchNotifications('deleteReadCompleted') } catch (error) { setNotifications(snapshot); throw error }
    },
  }), [data.issues])

  const visibleNotifications = useMemo(() => {
    const now = Date.now()
    let visible = notifications.filter(notification => {
      if (!displayOptions.showSnoozed && notification.snoozedUntil && new Date(notification.snoozedUntil).getTime() > now) return false
      return (displayOptions.showRead || !notification.read) && filters.every(filter => notificationMatchesFilter(notification, filter))
    })
    visible = [...visible].sort((left, right) => {
      if (displayOptions.showUnreadFirst && left.read !== right.read) return left.read ? 1 : -1
      if (displayOptions.ordering === 'priority') return sortablePriority(left.issuePriority) - sortablePriority(right.issuePriority) || new Date(right.timestamp).getTime() - new Date(left.timestamp).getTime()
      const delta = new Date(right.timestamp).getTime() - new Date(left.timestamp).getTime()
      return displayOptions.ordering === 'newest' ? delta : -delta
    })
    return visible
  }, [displayOptions, filters, notifications])
  const filterHiddenCount = useMemo(() => {
    const withoutFilters = notifications.filter(notification => notificationVisibleForDisplay(notification, displayOptions))
    return Math.max(0, withoutFilters.length - withoutFilters.filter(notification => filters.every(filter => notificationMatchesFilter(notification, filter))).length)
  }, [displayOptions, filters, notifications])

  const updateVisible = (nextVisible: InboxNotificationRowData[]) => {
    const visibleIds = new Set(visibleNotifications.map(item => item.id))
    const nextById = new Map(nextVisible.map(item => [item.id, item as InboxProjection]))
    setNotifications(current => [
      ...current.filter(item => !visibleIds.has(item.id)),
      ...nextVisible.map(item => nextById.get(item.id) as InboxProjection),
    ])
  }

  return <InboxPage
    notifications={visibleNotifications}
    selectedId={selectedId}
    onNotificationsChange={updateVisible}
    onSelectedIdChange={setSelectedId}
    adapter={adapter}
    displayOptions={displayOptions}
    onDisplayOptionsChange={setDisplayOptions}
    filters={filters}
    filterOptions={filterOptions}
    filterHiddenCount={filterHiddenCount}
    onFiltersChange={setFilters}
    onOpenSidebar={onOpenSidebar}
    onRetryLoad={() => void onReload()}
    onOpenIssue={notification => {
      const projection = notifications.find(item => item.id === notification.id)
      const issue = projection ? issueById.get(projection.issueId) : undefined
      const project = projection?.projectId ? data.projects.find(item => item.id === projection.projectId) : undefined
      if (issue) onOpenIssue(issue)
      else if (project) onOpenProject?.(project)
      else if (projection?.reviewId) {
        const review = data.reviews.find(item => item.id === projection.reviewId)
        if (review) onOpenReview?.(review)
      }
    }}
    onCopyIdentifier={notification => void copyText(notification.identifier)}
    onCopyLink={onCopyIssueLink ? notification => {
      const projection = notifications.find(item => item.id === notification.id)
      const issue = projection ? issueById.get(projection.issueId) : undefined
      if (issue) void onCopyIssueLink(issue)
    } : undefined}
    subscribed={notification => {
      const projection = notifications.find(item => item.id === notification.id)
      const issue = projection ? issueById.get(projection.issueId) : undefined
      return Boolean(issue?.subscriberIds.includes(data.viewer.id))
    }}
    onSubscribeChange={onSubscriberChange ? async (notification, subscribed) => {
      const projection = notifications.find(item => item.id === notification.id)
      const issue = projection ? issueById.get(projection.issueId) : undefined
      if (!issue) throw new Error('The source issue is no longer available.')
      await onSubscriberChange(issue, subscribed)
    } : undefined}
    renderDetail={notification => {
      const projection = notifications.find(item => item.id === notification.id)
      const issue = projection ? issueById.get(projection.issueId) : undefined
      const project = projection?.projectId ? data.projects.find(item => item.id === projection.projectId) : undefined
      const review = projection?.reviewId ? data.reviews.find(item => item.id === projection.reviewId) : undefined
      if (projection && review && !issue) return { content: <InboxReviewDetail review={review} onOpen={() => onOpenReview?.(review)} /> }
      if (projection && project && !issue) return { content: <ProjectReminderDetail overdue={project.health==='noUpdate'} project={project} onOpen={() => onOpenProject?.(project)}/> }
      if (!projection || !issue) return { content: <InboxMissingIssue /> }
      return {
        content: <DetailPane
          key={issue.id}
          issue={issue}
          data={data}
          presence={presence}
          workspacePresence={presence}
          comments={data.comments[issue.id] ?? []}
          activities={data.activities[issue.id] ?? []}
          embedded
          onClose={() => setSelectedId(null)}
          onNavigateIssue={onOpenIssue}
          onUpdate={input => onUpdateIssue?.(issue, input) ?? Promise.resolve()}
          onDelete={() => onDeleteIssue?.(issue) ?? Promise.resolve()}
          onCreateSubIssue={input => onCreateSubIssue(issue, input)}
          onReactIssue={emoji => onReactIssue(issue, emoji)}
          onComment={(body, bodyData, parentId) => onCreateComment(issue, body, bodyData, parentId)}
          onEditComment={(commentId, body, bodyData) => onEditComment(issue, commentId, body, bodyData)}
          onDeleteComment={commentId => onDeleteComment(issue, commentId)}
          onReactComment={(commentId, emoji) => onReactComment(issue, commentId, emoji)}
          onRelation={(type, relatedIssueId) => onCreateRelation?.(issue, type, relatedIssueId) ?? Promise.resolve()}
          onDeleteRelation={relationId => onDeleteRelation(issue, relationId)}
          onUpload={file => onUploadAttachment(issue, file)}
          onDeleteAttachment={attachmentId => onDeleteAttachment(issue, attachmentId)}
        />,
        fullBleed: true,
        issue,
        issues: data.issues,
        onUpdateIssue: onUpdateIssue ? input => onUpdateIssue(issue, input) : undefined,
        onDeleteIssue: onDeleteIssue ? () => onDeleteIssue(issue) : undefined,
        onCreateRelation: onCreateRelation ? (type, relatedIssueId) => onCreateRelation(issue, type, relatedIssueId) : undefined,
      }
    }}
  />
}

function readInboxFilters(): InboxFilterCondition[] {
  if (typeof window === 'undefined') return []
  const raw = new URL(window.location.href).searchParams.get('filter')
  if (!raw) return []
  try {
    const decoded = decodeInboxFilters(raw)
    const allowed = new Set<InboxFilterCondition['property']>(['notificationType', 'from', 'project', 'initiative', 'issuePriority', 'issueStatusType', 'reviewStatus'])
    const values = Array.isArray(decoded) ? decoded.filter(item => item && allowed.has(item.property) && Array.isArray(item.values)) : []
    return normalizeInboxFilters(values)
  } catch {
    return []
  }
}

function encodeInboxFilters(filters: InboxFilterCondition[]) {
  const bytes = new TextEncoder().encode(JSON.stringify(filters))
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '')
}

function decodeInboxFilters(value: string): unknown {
  const normalized = value.replaceAll('-', '+').replaceAll('_', '/')
  const binary = atob(normalized.padEnd(Math.ceil(normalized.length / 4) * 4, '='))
  const bytes = Uint8Array.from(binary, character => character.charCodeAt(0))
  return JSON.parse(new TextDecoder().decode(bytes))
}

function readInboxDisplayOptions(): InboxDisplayOptions {
  if (typeof window === 'undefined') return initialDisplayOptions
  try {
    const value = JSON.parse(window.localStorage.getItem('flow.inbox.display-options') ?? '') as Partial<InboxDisplayOptions>
    if ((value.ordering === 'newest' || value.ordering === 'oldest' || value.ordering === 'priority') && typeof value.showSnoozed === 'boolean' && typeof value.showRead === 'boolean' && typeof value.showUnreadFirst === 'boolean') {
      return value as InboxDisplayOptions
    }
  } catch {
    // Keep the known-good defaults when an older or malformed preference exists.
  }
  return initialDisplayOptions
}

function projectInbox(data: BootstrapData): InboxProjection[] {
  const issues = new Map(data.issues.map(issue => [issue.id, issue]))
  return data.notifications.flatMap<InboxProjection>(notification => {
    if (notification.reviewId) {
      const review = data.reviews.find(item => item.id === notification.reviewId)
      if (!review || notification.deletedAt || notification.archivedAt) return []
      return [{ id: notification.id, href: `/${data.workspace.urlKey}/review/${review.slugId}`, issueId: '', sourceType: 'activity' as const, sourceId: notification.sourceId, notificationType: 'review', actorId: notification.actor.id, actor: notification.actor.displayName, actorAvatarUrl: notification.actor.avatarUrl, kind: 'generic' as const, identifier: `${review.provider}#${review.number}`, title: review.title, body: `${notification.actor.displayName} requested your review`, timeLabel: relativeTime(notification.updatedAt), timestamp: notification.updatedAt, read: Boolean(notification.readAt), favorite: notification.favorite, initiativeIds: [], issuePriority: 0, issueStatusType: 'started' as const, reviewId: review.id, reviewStatus: review.draft ? 'draft' : review.status }]
    }
    const issue = notification.issueId ? issues.get(notification.issueId) : undefined
    const reminderProject = notification.projectId ? data.projects.find(project => project.id === notification.projectId) : undefined
    if (!issue && reminderProject && (notification.type === 'projectUpdateReminder' || notification.type === 'projectUpdateDueReminder') && !notification.deletedAt && !notification.archivedAt) {
      return [{
        id: notification.id,
        issueId: '',
        sourceType: 'activity' as const,
        sourceId: notification.sourceId,
        notificationType: 'reminder',
        actorId: notification.actor.id,
        actor: notification.actor.displayName,
        actorAvatarUrl: notification.actor.avatarUrl,
        kind: 'project' as const,
        identifier: 'Project update',
        title: reminderProject.name,
        body: notification.type === 'projectUpdateDueReminder' ? 'A project update is due soon' : 'A project update is overdue',
        timeLabel: relativeTime(notification.updatedAt),
        timestamp: notification.updatedAt,
        read: Boolean(notification.readAt),
        favorite: notification.favorite,
        snoozedUntil: notification.snoozedUntil,
        projectId: reminderProject.id,
        initiativeIds: data.initiatives.filter(initiative => initiative.projectIds.includes(reminderProject.id)).map(initiative => initiative.id),
        issuePriority: 0,
        issueStatusType: 'started' as const,
      }]
    }
    if (!issue || issue.archivedAt || notification.deletedAt || notification.archivedAt) return []
    const comment = notification.commentId ? (data.comments[issue.id] ?? []).find(item => item.id === notification.commentId) : undefined
    const event = notification.activityId ? (data.activities[issue.id] ?? []).find(item => item.id === notification.activityId) : undefined
    const sourceType = notification.sourceType === 'comment' ? 'comment' : 'activity'
    const sourceId = sourceType === 'comment' ? notification.commentId ?? notification.sourceId : notification.activityId ?? notification.sourceId
    return [{
      id: notification.id,
      issueId: issue.id,
      sourceType,
      sourceId,
      notificationType: comment ? 'comment' : event ? activityKind(event) : notification.type,
      actorId: notification.actor.id,
      actor: notification.actor.displayName,
      actorAvatarUrl: notification.actor.avatarUrl,
      kind: comment ? 'comment' : event ? activityKind(event) : notificationKind(notification),
      identifier: issue.identifier,
      title: issue.title,
      body: withOccurrence(comment ? `${notification.actor.displayName} commented: ${comment.body}` : event ? describeActivity(event, issue, data.viewer) : describeNotification(notification, issue), notification.occurrenceCount),
      timeLabel: relativeTime(notification.updatedAt),
      timestamp: notification.updatedAt,
      read: Boolean(notification.readAt),
      favorite: notification.favorite,
      snoozedUntil: notification.snoozedUntil,
      projectId: issue.project?.id,
      initiativeIds: issue.project ? data.initiatives.filter(initiative => initiative.projectIds.includes(issue.project!.id)).map(initiative => initiative.id) : [],
      issuePriority: issue.priority,
      issueState: issue.state,
      issueStatusType: issue.state.type,
    }]
  })
}

function withOccurrence(body: string, count: number) { return count > 1 ? `${body} · ${count} updates` : body }

function activityKind(event: ActivityEvent): InboxNotificationKind {
  if (event.type.startsWith('comment.')) return 'comment'
  if (event.type === 'issue.updated' && 'assigneeId' in event.metadata) return 'assignment'
  if (event.type.startsWith('project.')) return 'project'
  return 'status'
}

function notificationKind(notification: Notification): InboxNotificationKind {
  if (notification.type === 'comment') return 'comment'
  if (notification.type === 'assignment') return 'assignment'
  if (notification.type === 'mention') return 'mention'
  if (notification.type === 'project' || notification.type === 'projectUpdateReminder' || notification.type === 'projectUpdateDueReminder') return 'project'
  return 'status'
}

function describeNotification(notification: Notification, issue: Issue) {
  if (notification.type === 'assignment') return `${notification.actor.displayName} assigned the issue to you`
  if (notification.type === 'mention') return `${notification.actor.displayName} mentioned you in ${issue.identifier}`
  return `${notification.actor.displayName} updated the issue`
}

function describeActivity(event: ActivityEvent, issue: Issue, viewer: User) {
  const actor = event.actor.displayName
  if (event.type === 'issue.created') return `${actor} created the issue`
  if (event.type === 'comment.created') return `${actor} commented on the issue`
  if (event.type === 'comment.updated') return `${actor} edited a comment`
  if (event.type === 'comment.deleted') return `${actor} deleted a comment`
  if (event.type === 'attachment.created') return `${actor} attached ${event.metadata.title || 'a file'}`
  if (event.type === 'attachment.deleted') return `${actor} removed an attachment`
  if (event.type === 'issue.relation_added') return `${actor} added an issue relation`
  if (event.type === 'issue.relation_removed') return `${actor} removed an issue relation`
  if (event.type === 'issue.updated' && event.metadata.assigneeId === viewer.id) return `${actor} assigned the issue to you`
  if (event.type === 'issue.updated' && event.metadata.stateId) return `${actor} moved the issue to ${issue.state.name}`
  if (event.type === 'issue.updated') return `${actor} updated the issue`
  return `${actor} ${event.type.replaceAll('.', ' ').replaceAll('_', ' ')}`
}

function InboxMissingIssue() {
  return <div className="flow-inbox-detail-state flow-inbox-detail-state--error" role="alert"><strong>Issue is no longer available</strong></div>
}

function InboxReviewDetail({ review, onOpen }: { review: CodeReview; onOpen: () => void }) {
  return <div className="flow-inbox-review-detail"><header><span className={`flow-inbox-review-provider ${review.provider}`}>{review.provider === 'github' ? 'GH' : 'GL'}</span><div><small>{review.provider === 'github' ? 'GitHub pull request' : 'GitLab merge request'}</small><h2>{review.title}</h2></div></header><p>{review.author.displayName} · {review.repositoryName || 'External repository'} #{review.number}</p><dl><div><dt>Status</dt><dd>{review.status}</dd></div><div><dt>Reviewers</dt><dd>{review.reviewerIds.length || 'None'}</dd></div><div><dt>Checks</dt><dd>{review.checks.length ? review.checks.map(check => check.status).join(', ') : 'No checks reported'}</dd></div></dl><button type="button" onClick={onOpen}>Open review</button></div>
}

function ProjectReminderDetail({ project, onOpen, overdue }: { project: Project; onOpen: () => void; overdue: boolean }) {
  return <div className="flow-inbox-project-reminder"><header><span style={{ background: project.color }}/><div><small>Project update reminder</small><h2>{project.name}</h2></div></header><p>{overdue?'This project is missing its scheduled update.':'The next project update is due soon.'} Post an update to reset the cadence and notify subscribers.</p><dl><div><dt>Health</dt><dd>{overdue?'No update':project.health.replace(/([A-Z])/g,' $1')}</dd></div><div><dt>Status</dt><dd>{project.status.name}</dd></div>{project.targetDate&&<div><dt>Target date</dt><dd>{project.targetDate}</dd></div>}</dl><button type="button" onClick={onOpen}>Open project</button></div>
}

function relativeTime(value: string) {
  const elapsed = Math.max(0, Date.now() - new Date(value).getTime())
  const minute = 60_000
  const hour = 60 * minute
  const day = 24 * hour
  if (elapsed < hour) return `${Math.max(1, Math.floor(elapsed / minute))}m`
  if (elapsed < day) return `${Math.floor(elapsed / hour)}h`
  if (elapsed < 30 * day) return `${Math.floor(elapsed / day)}d`
  if (elapsed < 365 * day) return `${Math.floor(elapsed / (30 * day))}mo`
  return `${Math.floor(elapsed / (365 * day))}y`
}

async function copyText(value: string) {
  await navigator.clipboard?.writeText(value)
}

function putProjection(items: InboxProjection[], next: InboxProjection) {
  const index = items.findIndex(item => item.id === next.id)
  if (index < 0) return [...items, next]
  return items.map(item => item.id === next.id ? next : item)
}

function sortablePriority(priority: number) {
  return priority === 0 ? Number.MAX_SAFE_INTEGER : priority
}

function notificationMatchesFilter(notification: InboxProjection, filter: InboxFilterCondition) {
  const values = new Set(filter.values.map(value => value.value))
  if (!values.size) return true
  let match = false
  if (filter.property === 'notificationType') match = values.has(notification.notificationType)
  if (filter.property === 'from') match = values.has(notification.actorId)
  if (filter.property === 'project') match = notification.projectId ? values.has(notification.projectId) : values.has('__none__')
  if (filter.property === 'initiative') match = notification.initiativeIds.length ? notification.initiativeIds.some(id => values.has(id)) : values.has('__none__')
  if (filter.property === 'issuePriority') match = values.has(String(notification.issuePriority))
  if (filter.property === 'issueStatusType') match = values.has(notification.issueStatusType)
  if (filter.property === 'reviewStatus') match = notification.reviewStatus ? values.has(notification.reviewStatus) : values.has('__none__')
  return filter.operator === 'is' ? match : !match
}

function notificationVisibleForDisplay(notification: InboxProjection, display: InboxDisplayOptions) {
  if (!display.showRead && notification.read) return false
  return display.showSnoozed || !notification.snoozedUntil || new Date(notification.snoozedUntil).getTime() <= Date.now()
}

const notificationTypeOptions = [
  ['assignment', 'Assignments', 'assigned assignment'],
  ['comment', 'Comments and replies', 'commented reply comment'],
  ['apps', 'Apps and integrations', 'app integration'],
  ['customerRequest', 'Customer requests', 'customer request'],
  ['document', 'Document changes', 'document change'],
  ['loop', 'Loops', 'loop'],
  ['mention', 'Mentions', 'mentioned mention'],
  ['pulse', 'Pulse summaries', 'pulse summary'],
  ['reaction', 'Reactions', 'reaction emoji'],
  ['reminder', 'Reminders and deadlines', 'reminder deadline due date'],
  ['review', 'Reviews', 'review'],
  ['status', 'Status changes', 'status state issue'],
  ['subscription', 'Subscriptions', 'subscription subscribed'],
  ['system', 'System notifications', 'system'],
  ['triage', 'Triage', 'triage'],
  ['project', 'Updates', 'project update'],
] as const

function buildInboxFilterOptions(notifications: InboxProjection[], filters: InboxFilterCondition[], display: InboxDisplayOptions, data: BootstrapData): InboxFilterOptions {
  const candidatesFor = (property: InboxFilterCondition['property']) => notifications.filter(notification =>
    notificationVisibleForDisplay(notification, display)
    && filters.filter(filter => filter.property !== property).every(filter => notificationMatchesFilter(notification, filter)),
  )
  const count = (property: InboxFilterCondition['property'], value: string) => candidatesFor(property).filter(notification => notificationMatchesFilter(notification, {
    id: 'count', property, operator: 'is', values: [{ value, valueLabel: value }],
  })).length
  const statusDefinitions = [
    { id: 'triage', label: 'Triage', type: 'backlog' as const, color: 'var(--status-neutral)' },
    { id: 'backlog', label: 'Backlog', type: 'backlog' as const, color: 'var(--status-neutral)' },
    { id: 'unstarted', label: 'Unstarted', type: 'unstarted' as const, color: 'var(--status-neutral)' },
    { id: 'started', label: 'Started', type: 'started' as const, color: 'var(--data-vis-3)' },
    { id: 'completed', label: 'Completed', type: 'completed' as const, color: 'var(--accent-primary)' },
    { id: 'canceled', label: 'Canceled', type: 'canceled' as const, color: 'var(--status-neutral)' },
    { id: 'duplicate', label: 'Duplicate', type: 'canceled' as const, color: 'var(--status-neutral)' },
  ]

  return {
    notificationType: notificationTypeOptions.map(([id, label, keywords]) => ({ id, label, keywords, count: count('notificationType', id) })),
    from: data.users.map(user => ({ id: user.id, label: user.displayName, avatarUrl: user.avatarUrl, keywords: `${user.name} ${user.email}`, count: count('from', user.id), i18nIgnore: true })),
    project: [
      { id: '__none__', label: 'No project', keywords: 'none empty', icon: <NoProjectIcon size={15} />, count: count('project', '__none__') },
      ...data.projects.map(project => ({ id: project.id, label: project.name, color: project.color, icon: <ProjectIcon size={15} style={{ color: project.color }} />, count: count('project', project.id), i18nIgnore: true })),
    ],
    initiative: [
      { id: '__none__', label: 'No initiative', keywords: 'none empty', icon: <InitiativeGlyph />, count: count('initiative', '__none__') },
      ...data.initiatives.map(initiative => ({ id: initiative.id, label: initiative.name, color: initiative.color, icon: <InitiativeGlyph color={initiative.color} />, count: count('initiative', initiative.id), i18nIgnore: true })),
    ],
    issuePriority: ['No priority', 'Urgent', 'High', 'Medium', 'Low'].map((label, priority) => ({ id: String(priority), label, icon: <PriorityIcon priority={priority} size={15} />, count: count('issuePriority', String(priority)) })),
    issueStatusType: statusDefinitions.map(status => {
      const workspaceState = data.states.find(state => state.type === status.type)
      const state = status.id === 'duplicate'
        ? { id: 'duplicate', name: 'Duplicate', type: 'canceled' as const, color: status.color }
        : { id: status.id, name: status.label, type: status.type, color: workspaceState?.color ?? status.color }
      return { id: status.id, label: status.label, icon: <StatusIcon state={state} size={14} />, count: count('issueStatusType', status.id) }
    }),
    reviewStatus: INBOX_REVIEW_STATUS_OPTIONS.map(status => ({ id: status.id, label: status.label, color: status.color, count: count('reviewStatus', status.id) })),
  }
}

function InitiativeGlyph({ color = 'currentColor' }: { color?: string }) {
  return <svg width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden="true" style={{ color }}><path d="M3 2.25v11.5M4 3h7.25l-1.5 2.25 1.5 2.25H4" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /></svg>
}

function resolveSnoozedUntil(preset: InboxSnoozePreset): string {
  if (typeof preset === 'object') return preset.snoozedUntil
  const now = new Date()
  if (preset === 'hour') return new Date(now.getTime() + 60 * 60 * 1000).toISOString()

  const next = new Date(now)
  next.setSeconds(0, 0)
  next.setHours(9, 0, 0, 0)
  if (preset === 'tomorrow') {
    next.setDate(next.getDate() + 1)
  } else if (preset === 'nextWeek') {
    const daysUntilMonday = (8 - next.getDay()) % 7 || 7
    next.setDate(next.getDate() + daysUntilMonday)
  } else if (preset === 'month') {
    next.setMonth(next.getMonth() + 1)
  }
  return next.toISOString()
}
