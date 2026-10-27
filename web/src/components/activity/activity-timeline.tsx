import { format, formatDistanceToNow } from 'date-fns'
import { Archive, Bell, BellOff, CheckCircle2, Copy, Edit3, Ellipsis, Link2, Paperclip, RotateCcw, SmilePlus, Trash2 } from 'lucide-react'
import type { ActivityEvent, Comment, ThreadSubscription, ThreadSubscriptionState, WorkflowState } from '@/types/flow'
import { Avatar } from '@/components/issue/issue-row'
import { AttributedAction, ViaApp } from '@/components/ui/via-app'
import { activityActor, attributedActorAvatar, attributedActorName } from '@/lib/actor-attribution'
import { Composer } from '@/components/editor/composer'
import { useRef, useState, type ReactNode } from 'react'
import { hasAgentThread, useAgentCommentThreadIds, useStandaloneAgentSessions, type AgentThreadControls } from '@/components/agent/issue-agent-task-context'
import { toast } from '@/lib/toast'
import { useActivityHighlight, useActivityHighlightTarget, type ActivityHighlightTarget } from './activity-highlight'
import { EmojiPicker, ReactionPills } from '@/components/reactions/emoji-picker'
import { RichComment } from './rich-comment'
import { ResolvedComment } from './resolved-comment'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { useI18n } from '@/i18n/i18n'
import { useCurrentTimeMs } from '@/hooks/use-current-time'
import { CalendarIcon, CycleIcon, LabelIcon, PriorityIcon, ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import { activityTimeLabel, coalesceIssueEvents, describeIssueActivityParts, withoutSessionDelegations, issueEventFieldCount, type ActivityContext, type ActivityPart } from './issue-activity-model'
import { MentionReference } from '@/components/editor/mentions/mention-reference'
import { IntegrationBrandIcon } from '@/components/settings/integration-brand-icon'
import { inlineCommentsState } from '@/lib/inline-comments-state'
import './activity-timeline.css'

export function ActivityTimeline({
  events,
  comments,
  viewerId,
  context,
  highlightTarget,
  threadSummariesEnabled = false,
  parentId,
  parentType = 'issue',
  onReply,
  onEdit,
  onDelete,
  onReaction,
  onResolve,
  onUpload,
  threadSubscriptions,
  onThreadSubscription,
  renderAgentThread,
  renderAgentSession,
}: {
  events: ActivityEvent[]
  comments: Comment[]
  viewerId: string
  context?: ActivityContext
  highlightTarget?: ActivityHighlightTarget
  threadSummariesEnabled?: boolean
  parentId?: string
  parentType?: 'issue' | 'document'
  onReply: (body: string, bodyData: Record<string, unknown> | undefined, parentId: string) => Promise<void>
  onEdit: (id: string, body: string, bodyData?: Record<string, unknown>) => Promise<void>
  onDelete: (id: string) => Promise<void>
  onReaction: (id: string, emoji: string) => Promise<void>
  onResolve?: (id: string, resolved: boolean) => Promise<void>
  onUpload?: (file: File) => Promise<string>
  /** The viewer's explicit thread choices; participants follow threads implicitly. */
  threadSubscriptions?: ThreadSubscription[]
  onThreadSubscription?: (commentId: string, state: ThreadSubscriptionState | null) => Promise<void>
  /** threadIds: the thread's comment and its replies (a session a reply started belongs to the thread too). */
  renderAgentThread?: (comment: Comment, controls: AgentThreadControls, threadIds: string[]) => ReactNode
  /** Renders an agent session that has no source comment (a delegation); it sits at its chronological position. */
  renderAgentSession?: (taskId: string) => ReactNode
}) {
  const { t } = useI18n()
  const now = useCurrentTimeMs()
  const agentCommentIds = useAgentCommentThreadIds()
  const standaloneSessions = useStandaloneAgentSessions()
  const [threadOverrides, setThreadOverrides] = useState<Record<string, ThreadSubscriptionState | null>>({})
  const explicitThreadState = (rootId: string): ThreadSubscriptionState | null => rootId in threadOverrides
    ? threadOverrides[rootId]
    : threadSubscriptions?.find(item => item.commentId === rootId && item.userId === viewerId)?.state ?? null
  const changeThread = onThreadSubscription
    ? (rootId: string, state: ThreadSubscriptionState | null) => void run(rootId, async () => {
      await onThreadSubscription(rootId, state)
      setThreadOverrides(current => ({ ...current, [rootId]: state }))
    }).catch(error => toast.error(error instanceof Error ? error.message : t('Could not update thread subscription')))
    : undefined
  /** The thread's subscription and resolve controls, repeated in the menu of the agent replies inside it. */
  const agentThreadControls = (item: Comment): AgentThreadControls => {
    const state = explicitThreadState(item.id)
    const participating = item.user.id === viewerId || Boolean(replies.get(item.id)?.some(reply => reply.user.id === viewerId))
    const following = state === 'subscribed' || (participating && state !== 'muted')
    return {
      following,
      onToggleSubscription: changeThread ? () => changeThread(item.id, following ? (participating ? 'muted' : null) : 'subscribed') : undefined,
      resolved: Boolean(item.resolved),
      onResolve: onResolve ? () => void run(item.id, async () => {
        await onResolve(item.id, !item.resolved)
        if (parentId) inlineCommentsState.resolveMark(parentType, parentId, item.id, !item.resolved)
      }) : undefined,
      parentIssueId: parentType === 'issue' ? parentId : undefined,
    }
  }
  const [replying, setReplying] = useState<string | null>(null)
  const [editing, setEditing] = useState<string | null>(null)
  const [deleting, setDeleting] = useState<Comment | null>(null)
  const [busy, setBusy] = useState<string | null>(null)
  const [expanded, setExpanded] = useState(false)
  const { target, highlightAnchor } = useActivityHighlightTarget(highlightTarget)
  const timelineRef = useRef<HTMLDivElement>(null)
  const commentIds = new Set(comments.map((comment) => comment.id))
  const replies = new Map<string, Comment[]>()
  for (const comment of comments) {
    if (comment.parentId && commentIds.has(comment.parentId)) {
      const items = replies.get(comment.parentId) ?? []
      items.push(comment)
      replies.set(comment.parentId, items)
    }
  }
  const threadIdsOf = (rootId: string) => [rootId, ...(replies.get(rootId) ?? []).map((reply) => reply.id)]
  // Like the reference app, a thread reads oldest to newest with the reply box at the bottom, whatever order the page arrived in.
  for (const thread of replies.values()) thread.sort((a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime())
  const topLevel = comments.filter((comment) => !comment.parentId || !commentIds.has(comment.parentId))
  // Consecutive field changes by one actor read as one row ("Flow set priority to Urgent and set the estimate to 3
  // on behalf of Dev User"), unless a comment was posted between them.
  const commentTimes = topLevel.map((comment) => new Date(comment.createdAt).getTime())
  // A delegation's row gives way to the card of the session it started.
  const coalesced = coalesceIssueEvents(
    withoutSessionDelegations([...events], renderAgentSession ? standaloneSessions : []).sort((a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime()),
    (previous, next) => !commentTimes.some((time) => time > new Date(previous.createdAt).getTime() && time < new Date(next.createdAt).getTime()),
  )
  const eventItems = coalesced.flatMap((event) => {
    const description = describeIssueActivityParts(event, context, t)
    return description ? [{ ...event, description }] : []
  })
  const items = [
    ...eventItems.map((event) => ({ ...event, kind: 'event' as const })),
    ...topLevel.map((comment) => ({ ...comment, kind: 'comment' as const })),
    ...(renderAgentSession ? standaloneSessions.map((session) => ({ ...session, kind: 'agentSession' as const })) : []),
  ].sort((a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime())
  const targetComment = target?.kind === 'comment' ? comments.find((comment) => comment.id === target.id) : undefined
  const targetIndex = items.findIndex(
    (item) =>
      item.id === (targetComment?.parentId && commentIds.has(targetComment.parentId) ? targetComment.parentId : target?.id) &&
      (item.kind === (target?.kind === 'comment' ? 'comment' : 'event') || (target?.kind === 'comment' && item.kind === 'agentSession')),
  )
  const start = expanded ? 0 : Math.min(Math.max(0, items.length - 8), targetIndex < 0 ? items.length : targetIndex)
  const visible = items.slice(start)
  const hidden = start
  const contentKey = target?.kind === 'comment' ? targetComment?.id : targetIndex >= 0 ? target?.id : undefined
  useActivityHighlight(timelineRef, target, contentKey)
  const run = async (id: string, task: () => Promise<void>) => {
    setBusy(id)
    try {
      await task()
    } finally {
      setBusy(null)
    }
  }

  return (
    <>
      <div
        ref={timelineRef}
        className="timeline issue-activity-timeline"
        onClick={(event) => {
          const href = (event.target as Element).closest('a')?.getAttribute('href')
          if (!event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey && href?.match(/^#(activity|comment)-/)) {
            highlightAnchor(href)
          }
        }}
      >
        {hidden > 0 && (
          <button type="button" className="show-older-activity" onClick={() => setExpanded(true)}>
            Show {hidden} older activities
          </button>
        )}
        {visible.map((item) => item.kind === 'agentSession' ? (
          <div className="timeline-item agent-session" data-activity-anchor={`comment-${item.id}`} key={`agent-session-${item.id}`}>{renderAgentSession?.(item.id)}</div>
        ) : (
          <div
            className={`timeline-item ${item.kind}`}
            data-activity-anchor={item.kind === 'event' ? `activity-${item.id}` : undefined}
            id={item.kind === 'comment' ? `comment-${item.id}` : `activity-${item.id}`}
            key={`${item.kind}-${item.id}`}
          >
            {item.kind === 'event' ? (
              <>
                <div className="activity-event-icon">
                  <ActivityEventIcon event={item} context={context} />
                </div>
                <div>
                  <ActivityRow event={item} description={item.description} context={context} now={now} />
                </div>
              </>
            ) : (
              <ResolvedComment
                comment={item}
                threadSummariesEnabled={threadSummariesEnabled}
                busy={busy === item.id}
                onResolve={
                  onResolve
                    ? (resolved) =>
                        run(item.id, async () => {
                          await onResolve(item.id, resolved)
                          if (parentId) inlineCommentsState.resolveMark(parentType, parentId, item.id, resolved)
                        })
                    : undefined
                }
              >
                {/* The reference app comment thread: one card with the root comment, its replies and a reply input. */}
                <div className="comment-thread" data-has-replies={(replies.get(item.id) ?? []).length > 0 || undefined}>
                  <article className="comment-card" data-activity-anchor={`comment-${item.id}`}>
                    <header>
                      <Avatar name={attributedActorName(item.user, item.botActor)} avatarUrl={attributedActorAvatar(item.user, item.botActor)} />
                      <strong data-i18n-ignore>{attributedActorName(item.user, item.botActor)}</strong><ViaApp user={item.user} botActor={item.botActor} />
                      <a href={`#comment-${item.id}`} title={format(new Date(item.createdAt), 'PPpp')}>
                        {formatDistanceToNow(new Date(item.createdAt), { addSuffix: true })}
                      </a>
                      {item.editedAt && <span>{t('(edited)')}</span>}
                      <div className="comment-card__actions">
                        <EmojiPicker align="end" onSelect={(emoji) => run(item.id, () => onReaction(item.id, emoji))}>
                          <button type="button" aria-label="Add reaction">
                            <SmilePlus size={14} />
                          </button>
                        </EmojiPicker>
                        <CommentMenu
                          id={item.id}
                          own={item.user.id === viewerId}
                          body={item.body}
                          resolved={Boolean(item.resolved)}
                          thread={changeThread ? {
                            state: explicitThreadState(item.id),
                            participating: item.user.id === viewerId || Boolean(replies.get(item.id)?.some(reply => reply.user.id === viewerId)),
                            onChange: state => changeThread(item.id, state),
                          } : undefined}
                          onEdit={() => setEditing(item.id)}
                          onDelete={() => setDeleting(item)}
                          onResolve={
                            onResolve
                              ? () =>
                                  run(item.id, async () => {
                                    await onResolve(item.id, !item.resolved)
                                    if (parentId) inlineCommentsState.resolveMark(parentType, parentId, item.id, !item.resolved)
                                  })
                              : undefined
                          }
                        />
                      </div>
                    </header>
                    {editing === item.id ? (
                      <Composer
                        compact
                        initialValue={item.body}
                        initialData={item.bodyData}
                        placeholder="Edit comment…"
                        onUpload={onUpload}
                        onCancel={() => setEditing(null)}
                        onSubmit={async (body, data) => {
                          await onEdit(item.id, body, {
                            ...data,
                            ...(item.bodyData?.selection ? { selection: item.bodyData.selection } : {}),
                          })
                          setEditing(null)
                        }}
                      />
                    ) : (
                      <div className="comment-body">
                        <RichComment body={item.body} data={item.bodyData} version={item.version} />
                      </div>
                    )}
                    <ReactionPills
                      reactions={item.reactions}
                      viewerId={viewerId}
                      onToggle={(emoji) => run(item.id, () => onReaction(item.id, emoji))}
                    />
                  </article>
                  {(replies.get(item.id) ?? []).map((reply) => (
                    <article className="comment-card comment-reply" id={`comment-${reply.id}`} data-activity-anchor={`comment-${reply.id}`} key={reply.id}>
                      <header>
                        <Avatar name={attributedActorName(reply.user, reply.botActor)} avatarUrl={attributedActorAvatar(reply.user, reply.botActor)} />
                        <strong data-i18n-ignore>{attributedActorName(reply.user, reply.botActor)}</strong><ViaApp user={reply.user} botActor={reply.botActor} />
                        <a href={`#comment-${reply.id}`} title={format(new Date(reply.createdAt), 'PPpp')}>
                          {formatDistanceToNow(new Date(reply.createdAt), { addSuffix: true })}
                        </a>
                        {reply.editedAt && <span>{t('(edited)')}</span>}
                        <div className="comment-card__actions">
                          <EmojiPicker align="end" onSelect={(emoji) => run(reply.id, () => onReaction(reply.id, emoji))}>
                            <button type="button" aria-label="Add reaction">
                              <SmilePlus size={14} />
                            </button>
                          </EmojiPicker>
                          <CommentMenu
                            id={reply.id}
                            own={reply.user.id === viewerId}
                            body={reply.body}
                            onEdit={() => setEditing(reply.id)}
                            onDelete={() => setDeleting(reply)}
                          />
                        </div>
                      </header>
                      {editing === reply.id ? (
                        <Composer
                          compact
                          initialValue={reply.body}
                          initialData={reply.bodyData}
                          placeholder="Edit comment…"
                          onUpload={onUpload}
                          onCancel={() => setEditing(null)}
                          onSubmit={async (body, data) => { await onEdit(reply.id, body, data); setEditing(null) }}
                        />
                      ) : (
                        <div className="comment-body reply-body">
                          <RichComment body={reply.body} data={reply.bodyData} version={reply.version} />
                        </div>
                      )}
                      <ReactionPills
                        reactions={reply.reactions}
                        viewerId={viewerId}
                        onToggle={(emoji) => run(reply.id, () => onReaction(reply.id, emoji))}
                      />
                    </article>
                  ))}
                  {renderAgentThread && hasAgentThread(agentCommentIds, threadIdsOf(item.id)) ? renderAgentThread(item, agentThreadControls(item), threadIdsOf(item.id)) : replying === item.id ? (
                    <div className="comment-thread__composer">
                      <Composer
                        compact
                        placeholder="Leave a reply…"
                        onUpload={onUpload}
                        onCancel={() => setReplying(null)}
                        onSubmit={async (body, data) => {
                          await onReply(body, data, item.id)
                          setReplying(null)
                        }}
                      />
                    </div>
                  ) : (
                    <button type="button" className="comment-thread__reply" aria-label="Reply" onClick={() => setReplying(item.id)}>
                      <span>{t('Leave a reply…')}</span>
                    </button>
                  )}
                </div>
              </ResolvedComment>
            )}
          </div>
        ))}
      </div>
      <DeleteCommentDialog
        open={Boolean(deleting)}
        busy={busy === deleting?.id}
        onOpenChange={(open) => !open && setDeleting(null)}
        onConfirm={() => deleting && run(deleting.id, async () => {
          await onDelete(deleting.id)
          setDeleting(null)
        })}
      />
    </>
  )
}

/** Rows whose sentence names no actor ("Session with Flow archived"). */
const ACTORLESS_ACTIVITY = new Set(['issue.coding_session_archived'])

function ActivityRow({ event, description, context, now }: { event: ActivityEvent; description: ActivityPart[]; context?: ActivityContext; now: number }) {
  const { locale } = useI18n()
  const sentence = description.map((part, index) => typeof part === 'string' ? part : <MentionReference key={`${index}-${part.type}-${part.id}`} id={part.id} label={part.label} type={part.type}/>)
  return (
    <p className="activity-row" data-i18n-ignore>
      {ACTORLESS_ACTIVITY.has(event.type) && !event.metadata?.archivedBy ? sentence : <AttributedAction actor={activityActor(event.actor, event.botActor, context?.users)}>{sentence}</AttributedAction>}
      <span className="activity-time">
        <span className="activity-dot">·</span>
        <a href={`#activity-${event.id}`} title={format(new Date(event.createdAt), 'PPpp')}>
          <time dateTime={event.createdAt}>{activityTimeLabel(event.createdAt, now, locale)}</time>
        </a>
      </span>
    </p>
  )
}

function ActivityEventIcon({ event, context }: { event: ActivityEvent; context?: ActivityContext }) {
  const m = event.metadata ?? {}
  // Like the reference app, a row that changed several fields shows who did it rather than one field's icon.
  if (event.type === 'issue.updated' && issueEventFieldCount(event) < 2) {
    if (m.state)
      return (
        <StatusIcon
          size={14}
          state={
            context?.states.find((state) => state.id === m.stateId) ?? {
              id: m.stateId || m.state,
              name: m.state,
              color: 'currentColor',
              type: (m.stateType || 'unstarted') as WorkflowState['type'],
            }
          }
        />
      )
    if (m.priority) return <PriorityIcon size={14} priority={Math.max(0, ['No priority', 'Urgent', 'High', 'Medium', 'Low'].indexOf(m.priority))} />
    if ('labels' in m) return <LabelIcon size={14} />
    if ('project' in m || 'projectMilestone' in m) return <ProjectIcon size={14} />
    if ('cycle' in m) return <CycleIcon size={14} />
    if ('dueDate' in m) return <CalendarIcon size={14} />
    if ('title' in m) return <Edit3 size={14} />
  }
  if (event.type === 'issue.coding_session_archived') return <Archive size={14} />
  // A code host's own change (a closed pull request unassigned the agent): the host's mark.
  if (event.botActor?.type === 'integration') return <IntegrationBrandIcon provider={event.botActor.id === 'gitlab' ? 'gitlab' : 'github'} size={14}/>
  if (event.type.startsWith('attachment.')) return <Paperclip size={14} />
  if (event.type.includes('relation') || event.type.includes('review')) return <Link2 size={14} />
  const actor = activityActor(event.actor, event.botActor, context?.users)
  return <Avatar name={actor.name} avatarUrl={actor.avatarUrl} />
}

export function CommentMenu({
  id,
  own,
  body,
  resolved,
  thread,
  onEdit,
  onDelete,
  onResolve,
  triggerClassName,
  iconSize = 14,
}: {
  id: string
  own: boolean
  body: string
  triggerClassName?: string
  iconSize?: number
  resolved?: boolean
  thread?: { state: ThreadSubscriptionState | null; participating: boolean; onChange: (state: ThreadSubscriptionState | null) => void }
  onEdit: () => void
  onDelete: () => void
  onResolve?: () => void
}) {
  const { t } = useI18n()
  const following = thread ? thread.state === 'subscribed' || (thread.participating && thread.state !== 'muted') : false
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button type="button" className={triggerClassName} aria-label="Comment options">
          <Ellipsis size={iconSize} />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent className="comment-options" align="end">
        <DropdownMenuItem onSelect={() => void navigator.clipboard.writeText(`${location.href.split('#')[0]}#comment-${id}`)}>
          <Copy size={14} />
          Copy link to comment
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => void navigator.clipboard.writeText(body)}>
          <Copy size={14} />
          Copy content as Markdown
        </DropdownMenuItem>
        {thread && (
          <>
            <DropdownMenuSeparator />
            {following ? (
              <DropdownMenuItem onSelect={() => thread.onChange(thread.participating ? 'muted' : null)}>
                <BellOff size={14} />
                {t('Unsubscribe from thread')}
              </DropdownMenuItem>
            ) : thread.state === 'muted' ? (
              <DropdownMenuItem onSelect={() => thread.onChange(null)}>
                <Bell size={14} />
                {t('Unmute thread')}
              </DropdownMenuItem>
            ) : (
              <>
                <DropdownMenuItem onSelect={() => thread.onChange('subscribed')}>
                  <Bell size={14} />
                  {t('Subscribe to thread')}
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => thread.onChange('muted')}>
                  <BellOff size={14} />
                  {t('Mute thread')}
                </DropdownMenuItem>
              </>
            )}
          </>
        )}
        {onResolve && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={onResolve}>
              {resolved ? <RotateCcw size={14} /> : <CheckCircle2 size={14} />}
              {resolved ? 'Re-open thread' : 'Resolve thread'}
            </DropdownMenuItem>
          </>
        )}
        {own && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={onEdit}>
              <Edit3 size={14} />
              Edit comment
            </DropdownMenuItem>
            <DropdownMenuItem className="danger" onSelect={onDelete}>
              <Trash2 size={14} />
              Delete comment
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function DeleteCommentDialog({
  open,
  busy,
  onOpenChange,
  onConfirm,
}: {
  open: boolean
  busy: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: () => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="confirm-dialog">
        <DialogTitle>Delete comment?</DialogTitle>
        <p>This comment and its replies will be permanently deleted.</p>
        <footer>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button className="danger-button" disabled={busy} onClick={onConfirm}>
            {busy ? 'Deleting…' : 'Delete comment'}
          </Button>
        </footer>
      </DialogContent>
    </Dialog>
  )
}
