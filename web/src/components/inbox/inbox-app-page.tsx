import { workspaceFeatureEnabled } from '@/components/layout/sidebar-customization-state'
import { useEffect, useMemo, useRef, useState } from 'react'
import { toast } from 'sonner'
import { useRequestEntityUpdates } from '@/lib/entity-updates'
import { ProjectGlyph } from '@/components/views/project-glyph'

import type { ActivityEvent, Attachment, BootstrapData, CodeReview, Initiative, InitiativeUpdate, Issue, IssueRelationType, IssueUpdateInput, Notification, Presence, Project, ProjectUpdate, User } from '@/types/flow'
import { DetailPane } from '@/components/detail/detail-pane'
import { NoProjectIcon, PriorityIcon, WorkflowStatusGlyph } from '@/components/issue/issue-icons'
import type { SubIssueInput } from '@/components/issue/sub-issue-editor'
import { useI18n } from '@/i18n/i18n'
import { batchNotifications, updateInboxNotification, updateUserSettings } from '@/lib/api'
import { inboxUnread } from '@/lib/inbox-unread'
import {
  InitiativeOverviewInboxView,
  InitiativeUpdatesInboxView,
  ProjectOverviewInboxView,
  ProjectUpdatesInboxView,
  classifyInboxHost,
  notificationTypeMatchesPriorityRules,
  readPriorityInboxRuleState,
} from './hosts'

import { type InboxFilterCondition, type InboxFilterOptions } from './inbox-filter-builder'
import { INBOX_NOTIFICATION_TYPE_OPTIONS, INBOX_REVIEW_STATUS_OPTIONS, normalizeInboxFilters } from './inbox-filter-types'
import { inboxActorOptions, inboxNotificationCategory, matchesInboxFilter as notificationMatchesFilter } from './inbox-filter-model'
import { InboxPage, type InboxPageAdapter } from './inbox-page'
import type { InboxDisplayOptions, InboxTab } from './inbox-page-shell'
import type { InboxNotificationKind, InboxNotificationRowData, InboxSnoozePreset } from './notification-row'
import { effectivePulseFrequency, isPulseSummaryNotification, pulseSummaryText, pulseSummaryTitle, type PulseFrequency } from './pulse-summary-model'
import { PulseSummaryView } from './pulse-summary-view'
import { usePagedInbox } from './use-paged-inbox'
import { PullRequestInboxView } from '@/components/reviews/pull-request-inbox-view'
import { markdownPlainText } from '@/lib/markdown-plain-text'
import { DocumentInboxView, documentNotificationKind, documentNotificationLine, isDocumentNotification, notificationDocument } from './hosts/document-inbox-view'

const initialDisplayOptions: InboxDisplayOptions = {
  ordering: 'newest',
  showSnoozed: false,
  showRead: true,
  showUnreadFirst: false,
  priorityInbox: false,
  unreadGrouping: 'none',
}

interface InboxProjection extends InboxNotificationRowData {
  issueId: string
  sourceType: 'comment' | 'activity'
  sourceId: string
  notificationType: string
  actorId: string
  snoozedUntil?: string
  projectId?: string
  initiativeId?: string
  initiativeIds: string[]
  issuePriority: number
  issueStatusType: string
  reviewId?: string
  reviewStatus?: string
  updateId?: string
  hostKind?: ReturnType<typeof classifyInboxHost>
  /** Document notifications render the document host. */
  documentId?: string
}

export interface InboxAppPageProps {
  data: BootstrapData
  presence?: Presence[]
  onReload: () => Promise<void> | void
  onOpenIssue: (issue: Issue) => void
  onOpenProject?: (project: Project) => void
  onOpenInitiative?: (initiative: Initiative) => void
  onOpenReview?: (review: CodeReview) => void
  onOpenSettings?: () => void
  onCreateProjectUpdate?: (projectId: string, input: { body: string; health?: Project['health'] }) => Promise<ProjectUpdate | void>
  onCreateInitiativeUpdate?: (initiativeId: string, input: { body: string; health?: Project['health'] }) => Promise<InitiativeUpdate | void>
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
  onUploadAttachment: (issue: Issue, file: File, options?: { embed?: boolean }) => Promise<Attachment | void>
  onDeleteAttachment: (issue: Issue, attachmentId: string) => Promise<void>
  onCopyIssueLink?: (issue: Issue) => Promise<void> | void
  onOpenSidebar?: () => void
  activeTab?: InboxTab
  onTabChange?: (tab: InboxTab) => void
  /** Paged workspaces: load the full issue and its history into the app before showing it. */
  onLoadIssueContext?: (issueId: string) => Promise<void> | void
}

export function InboxAppPage({ data, presence = [], onReload, onOpenIssue, onOpenProject, onOpenInitiative, onOpenReview, onOpenSettings, onCreateProjectUpdate, onCreateInitiativeUpdate, onSubscriberChange, onUpdateIssue, onDeleteIssue, onCreateRelation, onDeleteRelation, onCreateSubIssue, onReactIssue, onCreateComment, onEditComment, onDeleteComment, onReactComment, onUploadAttachment, onDeleteAttachment, onCopyIssueLink, onOpenSidebar, activeTab = 'all', onTabChange, onLoadIssueContext }: InboxAppPageProps) {
  const { t } = useI18n()
  // Paged workspaces ship no notifications in the bootstrap: read them from the API.
  const paged = Boolean(data.issueCollectionPaged)
  const pagedInbox = usePagedInbox({ enabled: paged, workspaceKey: data.workspace.urlKey, live: data.notifications, loadedIssues: data.issues })
  const inboxData = useMemo(() => paged ? { ...data, notifications: pagedInbox.notifications, issues: pagedInbox.issues } : data, [data, paged, pagedInbox.issues, pagedInbox.notifications])
  const source = useMemo(() => projectInbox(inboxData, t), [inboxData, t])
  const savedPulseFrequency = effectivePulseFrequency(data)
  const [pulseFrequency, setPulseFrequency] = useState<PulseFrequency>(savedPulseFrequency)
  useEffect(() => setPulseFrequency(savedPulseFrequency), [savedPulseFrequency])
  const changePulseFrequency = async (next: PulseFrequency) => {
    const previous = pulseFrequency
    if (next === previous) return
    setPulseFrequency(next)
    try {
      await updateUserSettings({ pulseSchedule: next })
    } catch {
      setPulseFrequency(previous)
      toast.error(t('Couldn’t update Pulse frequency'))
    }
  }
  const issueById = useMemo(() => new Map(inboxData.issues.map(issue => [issue.id, issue])), [inboxData.issues])
  const [notifications, setNotifications] = useState<InboxProjection[]>(source)
  // Desktop/email notifications link to `/inbox?notification=<id>`.
  const [selectedId, setSelectedId] = useState<string | null>(() => typeof window === 'undefined' ? null : new URL(window.location.href).searchParams.get('notification'))
  const selectedProjection = selectedId ? notifications.find(item => item.id === selectedId) : undefined
  // Paged workspaces load the opened notification's project/initiative updates on demand.
  useRequestEntityUpdates('project', [selectedProjection?.projectId])
  useRequestEntityUpdates('initiative', [selectedProjection?.initiativeId ?? (selectedProjection?.hostKind?.startsWith('initiative') ? selectedProjection.sourceId : undefined)])
  // …and the opened issue's full record and history (the list only holds summaries).
  const selectedIssueId = selectedProjection?.issueId || undefined
  const selectedIssueSummary = Boolean(selectedIssueId && (issueById.get(selectedIssueId)?.isSummary || !data.issues.some(issue => issue.id === selectedIssueId)))
  const requestedContext = useRef(new Set<string>())
  useEffect(() => {
    if (!paged || !selectedIssueId || !selectedIssueSummary || !onLoadIssueContext || requestedContext.current.has(selectedIssueId)) return
    requestedContext.current.add(selectedIssueId)
    void Promise.resolve(onLoadIssueContext(selectedIssueId)).catch(() => requestedContext.current.delete(selectedIssueId))
  }, [onLoadIssueContext, paged, selectedIssueId, selectedIssueSummary])
  const [displayOptions, setDisplayOptions] = useState(readInboxDisplayOptions)
  const [filters, setFilters] = useState<InboxFilterCondition[]>(readInboxFilters)
  const filterOptions = useMemo<InboxFilterOptions>(
    () => buildInboxFilterOptions(notifications, displayOptions, inboxData),
    [inboxData, displayOptions, notifications],
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

  // Until the first paged load lands, keep a `?notification=` selection from a desktop/email link.
  const listPending = paged && pagedInbox.loading && !pagedInbox.notifications.length
  useEffect(() => {
    sourceByIdRef.current = new Map(source.map(notification => [notification.id, notification]))
    setNotifications(source)
    if (!listPending) setSelectedId(current => source.some(notification => notification.id === current) ? current : null)
  }, [listPending, source])

  const { upsert: upsertPaged, reload: reloadPaged } = pagedInbox
  const adapter = useMemo<InboxPageAdapter>(() => {
    // Paged rows live in the inbox's own list: apply the server's answer there and refresh the badge.
    const applied = (updated: Notification | undefined) => {
      if (!paged) return
      upsertPaged(updated)
      inboxUnread.schedule(data.workspace.urlKey, 0)
    }
    const batch = async (action: Parameters<typeof batchNotifications>[0]) => {
      await batchNotifications(action)
      if (paged) { await reloadPaged() }
    }
    return {
    setRead: async (id, read) => {
      applied(await updateInboxNotification(id, { read }))
    },
    delete: async id => {
      applied(await updateInboxNotification(id, { deleted: true }))
    },
    snooze: async (id, preset) => {
      const snoozedUntil = resolveSnoozedUntil(preset)
      applied(await updateInboxNotification(id, { snoozedUntil }))
      setNotifications(current => {
        const existing = current.find(notification => notification.id === id) ?? sourceByIdRef.current.get(id)
        return existing ? putProjection(current, { ...existing, snoozedUntil }) : current
      })
    },
    setFavorite: async (id, favorite) => {
      applied(await updateInboxNotification(id, { favorite }))
    },
    markAllRead: async () => {
      const snapshot = notificationsRef.current
      setNotifications(current => current.map(notification => ({ ...notification, read: true })))
      try { await batch('markAllRead') } catch (error) { setNotifications(snapshot); throw error }
    },
    deleteAll: async () => {
      const snapshot = notificationsRef.current
      setNotifications([])
      try { await batch('deleteAll') } catch (error) { setNotifications(snapshot); throw error }
    },
    deleteAllRead: async () => {
      const snapshot = notificationsRef.current
      setNotifications(current => current.filter(notification => !notification.read))
      try { await batch('deleteRead') } catch (error) { setNotifications(snapshot); throw error }
    },
    deleteAllReadCompleted: async () => {
      const completedIssueIds = new Set(inboxData.issues.filter(issue => issue.state.type === 'completed').map(issue => issue.id))
      const snapshot = notificationsRef.current
      setNotifications(current => current.filter(notification => !(notification.read && completedIssueIds.has(notification.issueId))))
      try { await batch('deleteReadCompleted') } catch (error) { setNotifications(snapshot); throw error }
    },
  }
  }, [data.workspace.urlKey, inboxData.issues, paged, reloadPaged, upsertPaged])

  const tabNotifications = useMemo(() => {
    const scope = activeTab === 'all' && displayOptions.priorityInbox ? 'priority' : activeTab
    return notifications.filter(notification => matchesInboxTab(notification, scope))
  }, [activeTab, displayOptions.priorityInbox, notifications])
  useEffect(() => {
    if (listPending) return
    const scope = activeTab === 'all' && displayOptions.priorityInbox ? 'priority' : activeTab
    // The list state lags a fresh source by one render: look the row up in either.
    setSelectedId(current => {
      if (!current) return current
      const projection = tabNotifications.find(notification => notification.id === current) ?? sourceByIdRef.current.get(current)
      return projection && matchesInboxTab(projection, scope) ? current : null
    })
  }, [activeTab, displayOptions.priorityInbox, listPending, tabNotifications])
  const visibleNotifications = useMemo(() => {
    const now = Date.now()
    let visible = tabNotifications.filter(notification => {
      if (!displayOptions.showSnoozed && notification.snoozedUntil && new Date(notification.snoozedUntil).getTime() > now) return false
      return (displayOptions.showRead || !notification.read) && filters.every(filter => notificationMatchesFilter(notification, filter))
    })
    visible = [...visible].sort((left, right) => {
      if ((displayOptions.showUnreadFirst || displayOptions.unreadGrouping === 'focus') && left.read !== right.read) return left.read ? 1 : -1
      if (displayOptions.ordering === 'priority') return sortablePriority(left.issuePriority) - sortablePriority(right.issuePriority) || new Date(right.timestamp).getTime() - new Date(left.timestamp).getTime()
      const delta = new Date(right.timestamp).getTime() - new Date(left.timestamp).getTime()
      return displayOptions.ordering === 'newest' ? delta : -delta
    })
    return visible
  }, [displayOptions, filters, tabNotifications])
  const filterHiddenCount = useMemo(() => {
    const withoutFilters = tabNotifications.filter(notification => notificationVisibleForDisplay(notification, displayOptions))
    return Math.max(0, withoutFilters.length - withoutFilters.filter(notification => filters.every(filter => notificationMatchesFilter(notification, filter))).length)
  }, [displayOptions, filters, tabNotifications])

  const tabCounts = useMemo(() => ({
    priority: notifications.filter(item => matchesInboxTab(item, 'priority') && !item.read && (!item.snoozedUntil || new Date(item.snoozedUntil).getTime() <= Date.now())).length,
    other: notifications.filter(item => matchesInboxTab(item, 'other') && !item.read && (!item.snoozedUntil || new Date(item.snoozedUntil).getTime() <= Date.now())).length,
  }), [notifications])

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
    onOpenSettings={onOpenSettings}
    onRetryLoad={() => void (paged ? reloadPaged() : onReload())}
    loading={paged && pagedInbox.loading && !pagedInbox.notifications.length}
    loadError={paged && pagedInbox.error && !pagedInbox.notifications.length}
    hasMore={paged && pagedInbox.hasMore}
    loadingMore={paged && pagedInbox.loadingMore}
    onLoadMore={paged ? () => void pagedInbox.loadMore() : undefined}
    onShowAllNotifications={() => {
      setFilters([])
      setDisplayOptions(current => ({ ...current, priorityInbox: false, showRead: true, showSnoozed: true, showUnreadFirst: false, unreadGrouping: 'none' }))
    }}
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
    pulseFrequency={pulseFrequency}
    onPulseFrequencyChange={changePulseFrequency}
    activeTab={activeTab}
    onTabChange={onTabChange}
    tabCounts={tabCounts}
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
      const initiative = projection?.initiativeId
        ? data.initiatives.find(item => item.id === projection.initiativeId)
        : data.initiatives.find(item => item.id === projection?.sourceId)
      const review = projection?.reviewId ? data.reviews.find(item => item.id === projection.reviewId) : undefined
      const hostKind = projection?.hostKind
        ?? (projection
          ? classifyInboxHost({
              type: projection.notificationType === 'reminder' ? (inboxData.notifications.find(item => item.id === projection.id)?.type ?? projection.notificationType) : (inboxData.notifications.find(item => item.id === projection.id)?.type ?? projection.notificationType),
              projectId: projection.projectId,
              issueId: projection.issueId || undefined,
              sourceType: projection.sourceType,
              sourceId: projection.sourceId,
              identifier: projection.identifier,
            })
          : 'other')

      if (projection?.documentId) {
        const raw = inboxData.notifications.find(item => item.id === projection.id)
        if (raw) return { content: <DocumentInboxView key={raw.id} data={data} notification={raw}/> }
      }

      if (projection?.kind === 'pulse') {
        return {
          content: <PulseSummaryView key={projection.id} notificationId={projection.id} title={projection.title} data={data} onOpenProject={onOpenProject} onOpenInitiative={onOpenInitiative} />,
          fullBleed: true,
        }
      }

      if (hostKind === 'project-updates' && project) {
        const updates = data.projectUpdates?.[project.id] ?? []
        const rawType = inboxData.notifications.find(item => item.id === projection!.id)?.type ?? ''
        return {
          content: (
            <ProjectUpdatesInboxView
              project={project}
              updates={updates}
              viewer={data.viewer}
              initialUpdateId={projection!.updateId ?? (projection!.sourceId && updates.some(item => item.id === projection!.sourceId) ? projection!.sourceId : undefined)}
              promptMode={/prompt|due|reminder/i.test(rawType) || project.health === 'noUpdate'}
              onOpenProject={() => onOpenProject?.(project)}
              onCreateUpdate={onCreateProjectUpdate ? input => onCreateProjectUpdate(project.id, input) : undefined}
            />
          ),
        }
      }

      if (hostKind === 'initiative-updates' && initiative) {
        const updates = data.initiativeUpdates?.[initiative.id] ?? []
        const rawType = inboxData.notifications.find(item => item.id === projection!.id)?.type ?? ''
        return {
          content: (
            <InitiativeUpdatesInboxView
              initiative={initiative}
              updates={updates}
              viewer={data.viewer}
              initialUpdateId={projection!.updateId ?? (projection!.sourceId && updates.some(item => item.id === projection!.sourceId) ? projection!.sourceId : undefined)}
              promptMode={/prompt|due|reminder/i.test(rawType) || initiative.health === 'noUpdate'}
              onOpenInitiative={() => onOpenInitiative?.(initiative)}
              onCreateUpdate={onCreateInitiativeUpdate ? input => onCreateInitiativeUpdate(initiative.id, input) : undefined}
            />
          ),
        }
      }

      if (hostKind === 'project-overview' && project) {
        const updates = data.projectUpdates?.[project.id] ?? []
        return {
          content: (
            <ProjectOverviewInboxView
              project={project}
              teams={data.teams}
              latestUpdate={updates[0]}
              onOpenProject={() => onOpenProject?.(project)}
            />
          ),
        }
      }

      if (hostKind === 'initiative-overview' && initiative) {
        const updates = data.initiativeUpdates?.[initiative.id] ?? []
        return {
          content: (
            <InitiativeOverviewInboxView
              initiative={initiative}
              teams={data.teams}
              projects={data.projects}
              latestUpdate={updates[0]}
              onOpenInitiative={() => onOpenInitiative?.(initiative)}
            />
          ),
        }
      }

      if (projection && review && !issue) return { content: <PullRequestInboxView data={data} review={review} onOpen={() => onOpenReview?.(review)} onReload={onReload} onAccessAction={() => onOpenSettings?.()} /> }
      // Reminder fallback still uses the compact reminder card when host classification did not claim the row.
      if (projection && project && !issue) return { content: <ProjectReminderDetail overdue={project.health==='noUpdate'} project={project} onOpen={() => onOpenProject?.(project)}/> }
      if (!projection || !issue) return { content: <InboxMissingIssue /> }
      return {
        content: <DetailPane
          key={issue.id}
          issue={issue}
          data={inboxData}
          presence={presence}
          workspacePresence={presence}
          comments={data.comments[issue.id] ?? []}
          activities={data.activities[issue.id] ?? []}
          highlightTarget={{ kind: projection.sourceType, id: projection.sourceId, key: projection.id }}
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
          onUpload={(file, options) => onUploadAttachment(issue, file, options)}
          onDeleteAttachment={attachmentId => onDeleteAttachment(issue, attachmentId)}
        />,
        fullBleed: true,
        issue,
        issues: inboxData.issues,
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
      return { ...initialDisplayOptions, ...value, priorityInbox: value.priorityInbox === true, unreadGrouping: value.unreadGrouping === 'focus' ? 'focus' : 'none' }
    }
  } catch {
    // Keep the known-good defaults when an older or malformed preference exists.
  }
  return initialDisplayOptions
}

function projectInbox(data: BootstrapData, t: (source: string) => string): InboxProjection[] {
  const pulseFrequency = effectivePulseFrequency(data)
  const issues = new Map(data.issues.map(issue => [issue.id, issue]))
  // Bootstrap may contain workspace-wide notification history in development
  // mode; Inbox itself is strictly recipient-scoped so every mutation maps to
  // a notification the current viewer can update.
  return data.notifications.filter(notification => notification.recipientId === data.viewer.id).flatMap<InboxProjection>(notification => {
    // Billing/usage alerts are intentionally excluded from Flow's product
    // surface; they belong to the removed commercial account area.
    if (notification.type === 'usageAlert') return []
    if (notification.type === 'welcomeMessage') {
      if (notification.deletedAt || notification.archivedAt) return []
      const content = (data.workspaceSettings.welcomeMessage ?? '').trim()
      return [{
        id: notification.id,
        issueId: '',
        sourceType: 'activity' as const,
        sourceId: notification.sourceId,
        notificationType: inboxNotificationCategory(notification),
        actorId: notification.actor.id,
        actor: notification.actor.displayName,
        actorAvatarUrl: notification.actor.avatarUrl,
        kind: 'generic' as const,
        identifier: 'Welcome',
        title: data.workspaceSettings.welcomeMessageTitle?.trim() || `Welcome to ${data.workspace.name}`,
        body: markdownPlainText(content.split('\n').find(line => line.trim()) ?? ''),
        timeLabel: relativeTime(notification.updatedAt),
        timestamp: notification.updatedAt,
        read: Boolean(notification.readAt),
        favorite: notification.favorite,
        snoozedUntil: notification.snoozedUntil,
        initiativeIds: [],
        issuePriority: 0,
        issueStatusType: 'started' as const,
      }]
    }
    if (notification.reviewId) {
      const review = data.reviews.find(item => item.id === notification.reviewId)
      if (!review || notification.deletedAt || notification.archivedAt) return []
      return [{ id: notification.id, href: `/${data.workspace.urlKey}/review/${review.slugId}`, issueId: '', sourceType: 'activity' as const, sourceId: notification.sourceId, notificationType: 'review', actorId: notification.actor.id, actor: notification.actor.displayName, actorAvatarUrl: notification.actor.avatarUrl, kind: 'review' as const, identifier: `${review.provider}#${review.number}`, title: review.title, body: `${notification.actor.displayName} requested your review`, timeLabel: relativeTime(notification.updatedAt), timestamp: notification.updatedAt, read: Boolean(notification.readAt), favorite: notification.favorite, snoozedUntil: notification.snoozedUntil, initiativeIds: [], issuePriority: 0, issueStatusType: 'started' as const, reviewId: review.id, reviewStatus: review.draft ? 'draft' : review.status }]
    }
    if (isPulseSummaryNotification(notification)) {
      if (notification.deletedAt || notification.archivedAt) return []
      return [{
        id: notification.id,
        issueId: '',
        sourceType: 'activity' as const,
        sourceId: notification.sourceId,
        notificationType: 'pulse',
        actorId: notification.actor.id,
        // Summaries come from Pulse itself, not a person.
        actor: '',
        kind: 'pulse' as const,
        identifier: '',
        title: t(pulseSummaryTitle(notification, pulseFrequency)),
        body: localizePulseText(pulseSummaryText(notification), t),
        timeLabel: relativeTime(notification.updatedAt),
        timestamp: notification.updatedAt,
        read: Boolean(notification.readAt),
        favorite: notification.favorite,
        snoozedUntil: notification.snoozedUntil,
        initiativeIds: [],
        issuePriority: 0,
        issueStatusType: 'started' as const,
        hostKind: 'other',
      }]
    }
    if (isDocumentNotification(notification) && !notification.issueId) {
      if (notification.deletedAt || notification.archivedAt) return []
      const document = notificationDocument(data, notification)
      return [{
        id: notification.id,
        issueId: '',
        sourceType: notification.commentId ? 'comment' as const : 'activity' as const,
        sourceId: notification.sourceId,
        notificationType: inboxNotificationCategory(notification),
        actorId: notification.actor.id,
        actor: notification.actor.displayName,
        actorAvatarUrl: notification.actor.avatarUrl,
        kind: documentNotificationKind(notification),
        identifier: notification.type === 'documentReminder' ? t('Reminder') : t('Document'),
        title: document?.title || notification.title || t('Untitled document'),
        body: documentNotificationLine(notification, t),
        timeLabel: relativeTime(notification.updatedAt),
        timestamp: notification.updatedAt,
        read: Boolean(notification.readAt),
        favorite: notification.favorite,
        snoozedUntil: notification.snoozedUntil,
        projectId: document?.projectIds[0],
        initiativeIds: [],
        issuePriority: 0,
        issueStatusType: 'started' as const,
        documentId: notification.sourceId,
      }]
    }
    const issue = notification.issueId ? issues.get(notification.issueId) : undefined
    const reminderProject = notification.projectId ? data.projects.find(project => project.id === notification.projectId) : undefined
    if (!issue && reminderProject && (/project/i.test(notification.type) || (/^update(Comment|Reaction)$/.test(notification.type) && notification.sourceType !== 'initiativeUpdate')) && !notification.deletedAt && !notification.archivedAt) {
      const hostKind = classifyInboxHost({
        type: notification.type,
        projectId: reminderProject.id,
        sourceType: notification.sourceType,
        sourceId: notification.sourceId,
      })
      const isUpdateHost = hostKind === 'project-updates' || /update/i.test(notification.type)
      return [{
        id: notification.id,
        issueId: '',
        sourceType: 'activity' as const,
        sourceId: notification.sourceId,
        notificationType: isUpdateHost || notification.type.includes('Update') ? 'project' : 'reminder',
        actorId: notification.actor.id,
        actor: notification.actor.displayName,
        actorAvatarUrl: notification.actor.avatarUrl,
        kind: 'project' as const,
        identifier: notification.type === 'projectReminder' ? 'Reminder' : 'Project update',
        title: reminderProject.name,
        // Server-written text (e.g. updateComment / updateReaction: "Ann commented on your update") wins.
        body: notification.text ? notification.text : notification.type === 'projectReminder'
          ? `${notification.actor.displayName} set a reminder`
          : notification.type === 'projectUpdateDueReminder'
            ? 'A project update is due soon'
            : /created|comment|mention|prompt/i.test(notification.type)
              ? `${notification.actor.displayName} shared a project update`
              : 'A project update is overdue',
        timeLabel: relativeTime(notification.updatedAt),
        timestamp: notification.updatedAt,
        read: Boolean(notification.readAt),
        favorite: notification.favorite,
        snoozedUntil: notification.snoozedUntil,
        projectId: reminderProject.id,
        initiativeIds: data.initiatives.filter(initiative => initiative.projectIds.includes(reminderProject.id)).map(initiative => initiative.id),
        issuePriority: 0,
        issueStatusType: 'started' as const,
        updateId: notification.sourceType === 'projectUpdate' ? notification.sourceId : undefined,
        hostKind: isUpdateHost ? 'project-updates' : hostKind === 'project-overview' ? 'project-overview' : 'project-updates',
      }]
    }
    // Notifications can target resources that are not represented in the
    // issue bootstrap (documents, loops, customer requests, integrations,
    // and product announcements). Keep them visible instead of silently
    // dropping them from the inbox; the detail surface can still be opened by
    // a future resource-specific handler.
    if (!issue && !reminderProject && !notification.deletedAt && !notification.archivedAt) {
      const initiative = data.initiatives.find(item => item.id === notification.sourceId)
        ?? (notification.sourceType === 'initiative' ? data.initiatives.find(item => item.id === notification.sourceId) : undefined)
        // updateComment / updateReaction on an initiative update name the initiative in the payload.
        ?? (notification.sourceType === 'initiativeUpdate' ? data.initiatives.find(item => item.id === notification.payload?.updates?.[0]?.sourceId) : undefined)
      const hostKind = classifyInboxHost({
        type: notification.type,
        sourceType: notification.sourceType,
        sourceId: notification.sourceId,
      })
      const isInitiative = Boolean(initiative) || /initiative/i.test(notification.type) || notification.sourceType === 'initiative' || notification.sourceType === 'initiativeUpdate'
      return [{
        id: notification.id,
        issueId: '',
        sourceType: 'activity' as const,
        sourceId: notification.sourceId,
        notificationType: inboxNotificationCategory(notification),
        actorId: notification.actor.id,
        actor: notification.actor.displayName,
        actorAvatarUrl: notification.actor.avatarUrl,
        kind: isInitiative ? 'project' as const : 'generic' as const,
        identifier: notification.type === 'initiativeReminder' || notification.type === 'documentReminder' ? 'Reminder' : genericNotificationTitle(notification),
        title: notification.title || initiative?.name || (notification.sourceType === 'document' ? data.documents?.find(item => item.id === notification.sourceId)?.title : undefined) || genericNotificationTitle(notification),
        body: notification.text ? notification.text : notification.type === 'initiativeReminder' || notification.type === 'documentReminder'
          ? `${notification.actor.displayName} set a reminder`
          : /initiativeUpdate/i.test(notification.type)
            ? `${notification.actor.displayName} shared an initiative update`
            : withOccurrence(genericNotificationBody(notification), notification.occurrenceCount),
        timeLabel: relativeTime(notification.updatedAt),
        timestamp: notification.updatedAt,
        read: Boolean(notification.readAt),
        favorite: notification.favorite,
        snoozedUntil: notification.snoozedUntil,
        initiativeId: initiative?.id,
        initiativeIds: initiative ? [initiative.id] : [],
        issuePriority: 0,
        issueStatusType: 'started' as const,
        updateId: notification.sourceType === 'initiativeUpdate' ? notification.sourceId : undefined,
        hostKind: isInitiative
          ? (hostKind === 'initiative-overview' ? 'initiative-overview' : 'initiative-updates')
          : hostKind,
      }]
    }
    if (!issue || issue.archivedAt || notification.deletedAt || notification.archivedAt) return []
    const comment = notification.commentId ? (data.comments[issue.id] ?? []).find(item => item.id === notification.commentId) : undefined
    const event = notification.activityId ? (data.activities[issue.id] ?? []).find(item => item.id === notification.activityId) : undefined
    const sourceType = notification.commentId || notification.sourceType === 'comment' ? 'comment' : 'activity'
    const sourceId = sourceType === 'comment' ? notification.commentId ?? notification.sourceId : notification.activityId ?? notification.sourceId
    return [{
      id: notification.id,
      issueId: issue.id,
      sourceType,
      sourceId,
      notificationType: inboxNotificationCategory(notification, comment ? 'comment' : event ? activityKind(event) : notification.type),
      actorId: notification.actor.id,
      actor: notification.actor.displayName,
      actorAvatarUrl: notification.actor.avatarUrl,
      kind: comment ? 'comment' : event ? activityKind(event) : notificationKind(notification),
      identifier: issue.identifier,
      title: issue.title,
      body: withOccurrence(comment ? `${notification.actor.displayName} commented: ${markdownPlainText(comment.body)}` : event ? describeActivity(event, issue, data.viewer) : describeNotification(notification, issue), notification.occurrenceCount),
      timeLabel: relativeTime(notification.updatedAt),
      timestamp: notification.updatedAt,
      read: Boolean(notification.readAt),
      favorite: notification.favorite,
      snoozedUntil: notification.snoozedUntil,
      projectId: issue.project?.id,
      initiativeIds: issue.project ? data.initiatives.filter(initiative => initiative.projectIds.includes(issue.project!.id)).map(initiative => initiative.id) : [],
      issuePriority: issue.priority,
      issueState: issue.state,
      issueStatusType: issue.state.name.toLowerCase() === 'triage' ? 'triage' : issue.state.name.toLowerCase() === 'duplicate' ? 'duplicate' : issue.state.type,
    }]
  })
}

/** Localizes the server's summary line ("Update from X" / "A, B and N other updates"); source names stay as written. */
function localizePulseText(text: string, t: (source: string) => string) {
  const single = /^Update from (.+)$/.exec(text)
  if (single) return t('Update from {name}').replace('{name}', single[1])
  const many = /^(.+) and (\d+) other updates?$/.exec(text)
  if (many) return t(many[2] === '1' ? '{names} and 1 other update' : '{names} and {count} other updates').replace('{names}', many[1]).replace('{count}', many[2])
  return t(text)
}

function withOccurrence(body: string, count: number) { return count > 1 ? `${body} · ${count} updates` : body }

function activityKind(event: ActivityEvent): InboxNotificationKind {
  if (event.type.startsWith('comment.')) return 'comment'
  if (event.type === 'issue.updated' && ('assigneeId' in event.metadata || 'assignee' in event.metadata)) return 'assignment'
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
  if (notification.type === 'triage') return `New issue in triage from ${notification.actor.displayName}`
  return `${notification.actor.displayName} updated the issue`
}

function genericNotificationTitle(notification: Notification) {
  const type = notification.type.replaceAll(/([a-z])([A-Z])/g, '$1 $2').replaceAll(/[._-]+/g, ' ').trim()
  return type ? type.charAt(0).toUpperCase() + type.slice(1) : 'Notification'
}

function genericNotificationBody(notification: Notification) {
  const actor = notification.actor.displayName || 'Someone'
  const category = notification.category.replaceAll(/([a-z])([A-Z])/g, '$1 $2').replaceAll(/[._-]+/g, ' ').trim()
  return category ? `${actor} sent a ${category} notification` : `${actor} sent a notification`
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

function notificationVisibleForDisplay(notification: InboxProjection, display: InboxDisplayOptions) {
  if (!display.showRead && notification.read) return false
  return display.showSnoozed || !notification.snoozedUntil || new Date(notification.snoozedUntil).getTime() <= Date.now()
}

/** Priority membership follows LS-0715 rule groups when Priority inbox is on. */
function matchesInboxTab(notification: InboxProjection, tab: InboxTab) {
  if (tab === 'all') return true
  const rules = readPriorityInboxRuleState()
  const otherTypes = ['reaction', 'reactions', 'subscription', 'subscriptions', 'pulse', 'apps', 'integration', 'customerRequest', 'customerRequests', 'document', 'documents', 'loop', 'loops', 'system']
  const priority = notificationTypeMatchesPriorityRules(notification.notificationType, notification.kind, rules)
    || (!otherTypes.includes(notification.notificationType)
      && !notificationTypeMatchesPriorityRules(notification.notificationType, notification.kind, rules)
      && ['assignment', 'mention', 'comment', 'project'].includes(notification.kind)
      && (rules['assigned-to-you'] || rules['mentions'] || rules['replies'] || rules['project-updates']))
  return tab === 'priority' ? priority : !priority
}

function buildInboxFilterOptions(notifications: InboxProjection[], display: InboxDisplayOptions, data: BootstrapData): InboxFilterOptions {
  // Menu counts describe the Inbox collection before user predicates, so a
  // zero-result filter never hides the options needed to change it.
  const candidates = notifications.filter(notification => notificationVisibleForDisplay(notification, display))
  const count = (property: InboxFilterCondition['property'], value: string) => candidates.filter(notification => notificationMatchesFilter(notification, {
    id: 'count', property, operator: 'is', values: [{ value, valueLabel: value }],
  })).length
  const statusDefinitions = [
    { id: 'triage', label: 'Triage', type: 'backlog' as const, color: 'var(--inbox-status-triage)' },
    { id: 'backlog', label: 'Backlog', type: 'backlog' as const, color: 'var(--inbox-status-backlog)' },
    { id: 'unstarted', label: 'Unstarted', type: 'unstarted' as const, color: 'var(--inbox-status-unstarted)' },
    { id: 'started', label: 'Started', type: 'started' as const, color: 'var(--inbox-status-started)' },
    { id: 'completed', label: 'Completed', type: 'completed' as const, color: 'var(--inbox-status-completed)' },
    { id: 'canceled', label: 'Canceled', type: 'canceled' as const, color: 'var(--inbox-status-canceled)' },
    { id: 'duplicate', label: 'Duplicate', type: 'canceled' as const, color: 'var(--inbox-status-canceled)' },
  ]

  return {
    notificationType: INBOX_NOTIFICATION_TYPE_OPTIONS.map(option => ({ ...option, count: count('notificationType', option.id) })),
    from: inboxActorOptions(candidates, data.users),
    project: [
      { id: '__none__', label: 'No project', keywords: 'none empty', icon: <NoProjectIcon size={15} />, count: count('project', '__none__') },
      ...data.projects.map(project => ({ id: project.id, label: project.name, color: project.color, icon: <ProjectGlyph project={project} size={15} />, count: count('project', project.id), i18nIgnore: true })),
    ],
    // Like Linear, properties with nothing to filter on are left out of the menu.
    initiative: !workspaceFeatureEnabled(data.workspaceSettings?.featureFlags, 'initiatives') || !data.initiatives.length ? [] : [
      { id: '__none__', label: 'No initiative', keywords: 'none empty', icon: <InitiativeGlyph />, count: count('initiative', '__none__') },
      ...data.initiatives.map(initiative => ({ id: initiative.id, label: initiative.name, color: initiative.color, icon: <InitiativeGlyph color={initiative.color} />, count: count('initiative', initiative.id), i18nIgnore: true })),
    ],
    issuePriority: ['No priority', 'Urgent', 'High', 'Medium', 'Low'].map((label, priority) => ({ id: String(priority), label, icon: <PriorityIcon priority={priority} size={15} />, count: count('issuePriority', String(priority)) })),
    issueStatusType: statusDefinitions.map(status => {
      const state = status.id === 'duplicate'
        ? { id: 'duplicate', name: 'Duplicate', type: 'canceled' as const, color: status.color }
        : { id: status.id, name: status.label, type: status.type, color: status.color }
      return { id: status.id, label: status.label, color: status.color, icon: <WorkflowStatusGlyph state={state} size={14} />, count: count('issueStatusType', status.id) }
    }),
    reviewStatus: !notifications.some(notification => notification.kind === 'review') ? [] : INBOX_REVIEW_STATUS_OPTIONS.map(status => ({ id: status.id, label: status.label, color: status.color, count: count('reviewStatus', status.id) })),
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
