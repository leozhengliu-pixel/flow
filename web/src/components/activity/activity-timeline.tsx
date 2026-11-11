import { format } from 'date-fns'
import { Archive, ArrowUp, Bell, BellOff, CheckCircle2, ChevronRight, Copy, Edit3, Ellipsis, Info, Link2, Paperclip, RotateCcw, SmilePlus, Trash2 } from 'lucide-react'
import type { ActivityEvent, Comment, ThreadSubscription, ThreadSubscriptionState, WorkflowState } from '@/types/flow'
import { Avatar } from '@/components/issue/issue-row'
import { PersonCardHover } from '@/components/property/assignee-hover-card'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { AttributedAction, ViaApp } from '@/components/ui/via-app'
import { activityActor, attributedActorAvatar, attributedActorName, sameActivityActor } from '@/lib/actor-attribution'
import { Composer } from '@/components/editor/composer'
import { Fragment, useEffect, useRef, useState, type ReactNode } from 'react'
import * as Popover from '@radix-ui/react-popover'
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
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import { useDesktopLayout } from '@/hooks/use-desktop-layout'
import { useIssuesById } from '@/components/issue/use-issues-by-id'
import { CalendarIcon, CycleIcon, LabelIcon, PriorityIcon, ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import { issuePath, memberProfileHandle, memberProfilePath } from '@/lib/app-routes'
import { FlowTooltip } from '@/components/ui/tooltip'
import { activityExpandedTimeLabel, activityTimeLabel, issueActivityEntries, withoutSessionDelegations, type ActivityValue, type IssueActivityEntry, type ActivityContext, type ActivityPart } from './issue-activity-model'
import { MentionReference } from '@/components/editor/mentions/mention-reference'
import { IntegrationBrandIcon } from '@/components/settings/integration-brand-icon'
import { FlowLogo } from '@/components/ui/flow-logo'
import { LabelHoverPreview } from '@/components/property/label-hover-preview'
import { EstimateIcon } from '@/components/issue/estimate-icon'
import { inlineCommentsState } from '@/lib/inline-comments-state'
import { activityBlockExpanded, expandActivityBlock, activityCollapseReasons, groupIssueActivityEntries, partitionIssueActivity, type ActivityCollapsePosition } from './issue-event-groups'
import './activity-timeline.css'
import { CommentActions } from './comment-actions'
import { useCommentActions } from '@/hooks/use-comment-actions'

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
  const { t, locale } = useI18n()
  const commentActions = useCommentActions(parentType === 'issue' ? parentId : undefined, comments.map(comment => `${comment.id}:${comment.version}`).join(','), viewerId)
  const now = useCurrentTimeMs(30_000)
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
  const replyUser = context?.users.find(user => user.id === viewerId)
  const shownEvents = withoutSessionDelegations([...events], renderAgentSession ? standaloneSessions : [])
    .sort((a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime())
  const eventItems = shownEvents.flatMap(event => issueActivityEntries(event, context, t, new Date(now).getFullYear()))
  const items = [
    ...eventItems.map((event) => ({ ...event, kind: 'event' as const })),
    ...topLevel.map((comment) => ({ ...comment, kind: 'comment' as const })),
    ...(renderAgentSession ? standaloneSessions.map((session) => ({ ...session, kind: 'agentSession' as const })) : []),
  ].sort((a, b) => a.kind === 'event' && a.type === 'issue.created' && !(b.kind === 'event' && b.type === 'issue.created') ? -1 : b.kind === 'event' && b.type === 'issue.created' && !(a.kind === 'event' && a.type === 'issue.created') ? 1 : new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime())
  const targetComment = target?.kind === 'comment' ? comments.find((comment) => comment.id === target.id) : undefined
  const targetIndex = items.findIndex(
    (item) =>
      item.id === (targetComment?.parentId && commentIds.has(targetComment.parentId) ? targetComment.parentId : target?.id) &&
      (item.kind === (target?.kind === 'comment' ? 'comment' : 'event') || (target?.kind === 'comment' && item.kind === 'agentSession')),
  )
  const blocks: (typeof items)[] = []
  for (const item of items) {
    const last = blocks.at(-1)
    if (item.kind === 'event' && last?.[0].kind === 'event') last.push(item)
    else blocks.push([item])
  }
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
        {blocks.map((block, index) => block[0].kind === 'event' ? <ActivityHistoryBlock key={block[0].id} entries={block.filter(item => item.kind === 'event')} context={context} now={now} position={blocks.length === 1 ? 'only' : index === 0 ? 'first' : index === blocks.length - 1 ? 'last' : 'middle'} nextCommentDate={blocks[index + 1]?.[0].createdAt} targetId={target?.id}/> : block.map((item) => {
          return <Fragment key={`${item.kind}-${item.id}`}>{item.kind === 'agentSession' ? (
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
                activitySummary={t('{count} resolved comment from {name}').replace('{count}', '1').replace('{name}', attributedActorName(item.user, item.botActor))}
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
                      <Avatar id={item.user.id} name={attributedActorName(item.user, item.botActor)} avatarUrl={attributedActorAvatar(item.user, item.botActor)} />
                      <strong data-i18n-ignore>{attributedActorName(item.user, item.botActor)}</strong><ViaApp user={item.user} botActor={item.botActor} />
                      <a href={`#comment-${item.id}`} title={format(new Date(item.createdAt), 'PPpp')}>
                        <span>{activityTimeLabel(item.createdAt, now, locale)}</span>
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
                    <CommentActions commentId={item.id} model={commentActions} context={context} />
                  </article>
                  {(replies.get(item.id) ?? []).map((reply) => (
                    <article className="comment-card comment-reply" id={`comment-${reply.id}`} data-activity-anchor={`comment-${reply.id}`} key={reply.id}>
                      <header>
                        <Avatar id={reply.user.id} name={attributedActorName(reply.user, reply.botActor)} avatarUrl={attributedActorAvatar(reply.user, reply.botActor)} />
                        <strong data-i18n-ignore>{attributedActorName(reply.user, reply.botActor)}</strong><ViaApp user={reply.user} botActor={reply.botActor} />
                        <a href={`#comment-${reply.id}`} title={format(new Date(reply.createdAt), 'PPpp')}>
                          {activityTimeLabel(reply.createdAt, now, locale)}
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
                      <CommentActions commentId={reply.id} model={commentActions} context={context} />
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
                  ) : replyUser ? (
                    // The idle desktop reply is a one-line composer: the placeholder, the attach button and a send button that waits for text.
                    <form className="comment-thread__reply" data-reply-idle="" onSubmit={event => event.preventDefault()} onClick={() => setReplying(item.id)}>
                      <Avatar id={replyUser.id} name={replyUser.displayName} avatarUrl={replyUser.avatarUrl}/>
                      <button type="button" className="comment-thread__reply-open" aria-label={t('Reply')}><span>{t('Leave a reply…')}</span></button>
                      <button type="button" className="comment-thread__reply-tool" aria-label={t('Attach images, files, or videos')}><Paperclip size={14}/></button>
                      <button type="submit" className="comment-thread__reply-send" aria-label={t('Submit comment')} disabled><ArrowUp size={14}/></button>
                    </form>
                  ) : (
                    <button type="button" className="comment-thread__reply" aria-label={t('Reply')} onClick={() => setReplying(item.id)}>
                      <span>{t('Leave a reply…')}</span>
                    </button>
                  )}
                </div>
              </ResolvedComment>
            )}
          </div>
        )}</Fragment>
        }))}
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
const SYSTEM_AUTOMATIONS = new Set(['autoArchive', 'autoClose', 'autoCloseStale', 'autoCycle'])

function ActivityRow({ event, description, context, now, longTime = false }: { event: ActivityEvent & Partial<IssueActivityEntry>; description: ActivityPart[]; context?: ActivityContext; now: number; longTime?: boolean }) {
  const { t, locale } = useI18n()
  const phone = usePhoneLayout()
  const desktop = useDesktopLayout()
  const inlineRelation = phone && event.type === 'issue.relation_added'
  const actor = activityActor(event.actor, event.botActor, context?.users)
  if (!actor.name || SYSTEM_AUTOMATIONS.has(event.metadata?.automation)) actor.name = 'Flow'
  const actorName = actor.name !== 'Flow' && !event.botActor && context?.workspace && event.actor?.id ? <PersonCardHover user={event.actor}><a href={memberProfilePath(context.workspace.urlKey, memberProfileHandle(event.actor))}>{actor.name}</a></PersonCardHover> : undefined
  const actors = event.members?.filter((member, index, members) => members.findIndex(other => sameActivityActor(other, member)) === index)
  const names = actors?.slice(0, actors.length > 3 ? 2 : 3).map(member => activityActor(member.actor, member.botActor, context?.users).name || 'Flow') ?? []
  if (actors && actors.length > 3) names.push(t('{count} others').replace('{count}', String(actors.length - 2)))
  const groupActors = names.map((name, index) => <Fragment key={index}>{index > 0 && (index === names.length - 1 ? t('{changes} and {last}').replace('{changes}', '').replace('{last}', '') : t('{changes}, {next}').replace('{changes}', '').replace('{next}', ''))}<strong>{name}</strong></Fragment>)
  const sentence = description.map((part, index) => typeof part === 'string' ? part : part.type === 'value' ? <ActivityValueContent key={index} part={part} previousTitle={event.entryType === 'title-change' ? event.metadata.titleBefore : undefined} source={event.metadata.linkSource}/> : part.type === 'label' ? <ActivityLabel key={index} part={part} context={context}/> : inlineRelation && part.type === 'issue' && context?.workspace
    ? (() => {
      const key = `${index}-${part.type}-${part.id}`
      const target = context.issues.find(issue => issue.id === part.id)
      if (!target) return <MentionReference key={key} id={part.id} label={part.label} type={part.type}/>
      const link = <a key={key} className="activity-relation-link" href={issuePath(context.workspace.urlKey, target)}><b>{target.identifier}</b> {target.title}</a>
      return event.metadata?.type === 'blocks' ? <span key={key} className="activity-blocking-target">{link}</span> : link
    })()
    : desktop && part.type === 'issue' && context?.workspace
      ? <ActivityIssueLink key={`${index}-${part.id}`} id={part.id} label={part.label} context={context}/>
      : <MentionReference key={`${index}-${part.type}-${part.id}`} id={part.id} label={part.label} type={part.type}/>)
  return (
    <p className="activity-row" data-i18n-ignore>
      {event.actorless || ACTORLESS_ACTIVITY.has(event.type) && !event.metadata?.archivedBy ? sentence : names.length > 1 ? <>{groupActors} {sentence}</> : <AttributedAction actor={actor} actorName={actorName}>{sentence}</AttributedAction>}
      {event.tooltip && <span className="activity-info" title={event.tooltip}><Info size={10}/></span>}
      <span className="activity-time">
        <span className="activity-dot">·</span>
        <FlowTooltip side="top" label={format(new Date(event.createdAt), 'EEE MMM d, yyyy, h:mm:ss a')}><a href={`#activity-${event.id}`} aria-label={format(new Date(event.createdAt), 'EEE MMM d, yyyy, h:mm:ss a')} onClick={async click => {
          click.preventDefault(); click.stopPropagation()
          await navigator.clipboard.writeText(`${location.href.split('#')[0]}#activity-${event.id}`)
          toast.success(t('Link to activity item copied to clipboard'))
        }}>
          <time dateTime={event.createdAt}>{longTime ? activityExpandedTimeLabel(event.createdAt, now, locale) : activityTimeLabel(event.createdAt, now, locale)}</time>
        </a></FlowTooltip>
      </span>
    </p>
  )
}

function ActivityValueContent({ part, previousTitle, source }: { part: ActivityValue; previousTitle?: string; source?: string }) {
  const { t } = useI18n()
  if (part.href) return <FlowTooltip side="top" label={source && source !== 'url' ? t('Open in {source}').replace('{source}', source) : t('Open link')}><a href={part.href} target="_blank" rel="noreferrer"><span className="activity-link-source">{source === 'github' || source === 'gitlab' ? <IntegrationBrandIcon provider={source} size={12}/> : <Link2 size={12}/>}</span><b>{part.label}</b></a></FlowTooltip>
  if (!part.title) return <b>{part.label}</b>
  return previousTitle ? <ActivityTitleValue part={part} previousTitle={previousTitle}/> : <FlowTooltip side="top" label={part.title}><b tabIndex={0}>{part.label}</b></FlowTooltip>
}

function ActivityTitleValue({ part, previousTitle }: { part: ActivityValue; previousTitle: string }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const trigger = useRef<HTMLElement>(null)
  const content = useRef<HTMLDivElement>(null)
  useEffect(() => () => clearTimeout(timer.current), [])
  const show = () => { clearTimeout(timer.current); setOpen(true) }
  const hide = () => {
    clearTimeout(timer.current)
    timer.current = setTimeout(() => {
      if (!content.current?.contains(document.activeElement) && document.activeElement !== trigger.current) setOpen(false)
    }, 150)
  }
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild><b ref={trigger} tabIndex={0} role="button" onFocus={show} onClick={event => { event.preventDefault(); show() }} onKeyDown={event => { if (['Enter', ' ', 'ArrowDown'].includes(event.key)) { event.preventDefault(); show(); requestAnimationFrame(() => content.current?.querySelector('button')?.focus()) } }} onMouseEnter={() => { clearTimeout(timer.current); timer.current = setTimeout(show, 450) }} onMouseLeave={hide}>{part.label}</b></Popover.Trigger>
    <Popover.Portal><Popover.Content ref={content} data-flow-motion="tooltip" className="flow-tooltip-content flow-tooltip-content--card activity-title-preview" side="top" sideOffset={8} collisionPadding={8} onOpenAutoFocus={event => event.preventDefault()} onCloseAutoFocus={event => event.preventDefault()} onMouseEnter={show} onMouseLeave={hide} onFocusOutside={event => { if (event.target === trigger.current) event.preventDefault() }}>
      <span className="flow-tooltip-copy">{part.title}</span><button type="button" className="activity-copy-title" aria-label={t('Copy previous title')} onClick={async () => { await navigator.clipboard.writeText(previousTitle); toast.success(t('Previous title copied to clipboard')) }}><Copy size={12}/></button>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

function ActivityLabel({ part, context }: { part: { id: string; label: string }; context?: ActivityContext }) {
  const label = context?.labels.find(item => item.id === part.id)
  const content = <><i style={{ backgroundColor: label?.color }}/><b>{part.label}</b></>
  return label?.archivedAt ? <span className="activity-label" data-archived>{content}</span> : label ? <LabelHoverPreview label={label}><a className="activity-label" data-agent-entity="label" href={`/${context?.workspace.urlKey}/issues?label=${encodeURIComponent(part.id)}`}>{content}</a></LabelHoverPreview> : <span className="activity-label">{content}</span>
}

function ActivityHistoryBlock({ entries, context, now, position, nextCommentDate, targetId }: { entries: IssueActivityEntry[]; context?: ActivityContext; now: number; position: ActivityCollapsePosition; nextCommentDate?: string; targetId?: string }) {
  const { t } = useI18n()
  const [expanded, setExpanded] = useState(false)
  const [wasLast] = useState(position === 'last' || position === 'only')
  const rows = groupIssueActivityEntries(entries, { now, last: wasLast }, t)
  const partition = partitionIssueActivity(rows, position, now, nextCommentDate)
  const blockKey = `${context?.workspace?.id ?? ''}:${entries[0].id}`
  const open = expanded || activityBlockExpanded(blockKey, now) || entries.some(row => row.id === targetId || row.recordId === targetId)
  const render = (row: IssueActivityEntry) => <ActivityHistoryEntry key={row.id} row={row} context={context} now={now} targetId={targetId}/>
  return <div className="activity-history-block" data-connected={rows.length >= 2 || undefined}>
    {(open ? rows : partition.before).map(render)}
    {!open && partition.hidden.length > 0 && <button type="button" className="timeline-item event activity-event-group" aria-label={t('Expand all activity')} aria-expanded={false} onClick={() => { expandActivityBlock(blockKey, now); setExpanded(true) }}><span className="activity-event-icon"><ChevronRight size={14}/></span><span className="activity-row">{t('Show {count} events').replace('{count}', String(partition.hidden.length))}{activityCollapseReasons(partition.hidden, t)}</span></button>}
    {!open && partition.after.map(render)}
  </div>
}

function ActivityHistoryEntry({ row, context, now, targetId, nested = false }: { row: IssueActivityEntry; context?: ActivityContext; now: number; targetId?: string; nested?: boolean }) {
  const [expanded, setExpanded] = useState(false)
  const open = expanded || Boolean(row.members?.some(member => member.id === targetId || member.recordId === targetId))
  const body = <><span className="activity-event-icon"><ActivityEventIcon event={row} context={context}/></span><span><ActivityRow event={row} description={row.description} context={context} now={now} longTime={nested}/></span></>
  return <div className="activity-history-entry">
    {row.members ? <div className="timeline-item event activity-family-header" role="button" tabIndex={0} aria-expanded={open} onClick={event => { if (!(event.target as Element).closest('a')) setExpanded(!open) }} onKeyDown={event => { if (event.target === event.currentTarget && (event.key === 'Enter' || event.key === ' ')) { event.preventDefault(); setExpanded(!open) } }}>{body}</div> : <div className="timeline-item event" id={`activity-${row.id}`} data-activity-anchor={`activity-${row.id}`}>{body}</div>}
    {open && row.members && <div className="activity-history-block activity-family-entries" data-connected={row.members.length >= 2 || undefined}>{row.members.map(member => <ActivityHistoryEntry key={member.id} row={member} context={context} now={now} nested/>)}</div>}
  </div>
}

function ActivityIssueLink({ id, label, context }: { id: string; label: string; context: ActivityContext }) {
  const targets = useIssuesById([id], context)
  const issue = targets.get(id)
  return <a className="activity-relation-link" href={issuePath(context.workspace.urlKey, issue ?? { identifier: label, title: '' })}>{issue ? <><b>{issue.identifier}</b> {issue.title.length > 36 ? `${issue.title.slice(0, 36)}…` : issue.title}</> : label}</a>
}

function ActivityEventIcon({ event, context }: { event: ActivityEvent & Partial<IssueActivityEntry>; context?: ActivityContext }) {
  const phone = usePhoneLayout()
  const desktop = useDesktopLayout()
  const m = event.metadata ?? {}
  // Like the reference app, a row that changed several fields shows who did it rather than one field's icon.
  if (event.type === 'issue.updated' && event.entryType !== 'aggregate') {
    if (event.entryType === 'state-change')
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
    if (event.entryType === 'priority') return <PriorityIcon size={14} priority={Math.max(0, ['No priority', 'Urgent', 'High', 'Medium', 'Low'].indexOf(m.priority))} />
    if (event.entryType?.startsWith('label')) return <LabelIcon size={14} />
    if (event.entryType === 'project' || event.entryType === 'milestone') return <ProjectIcon size={14} />
    if (event.entryType === 'cycle') return <CycleIcon size={14} />
    if (event.entryType === 'due-date') return <CalendarIcon size={14} />
    if (event.entryType === 'title-change') return <Edit3 size={14} />
    if (event.entryType === 'estimate') return <EstimateIcon size={14}/>
    if (event.entryType === 'parent') return <IssueActionGlyph label="Parent issue…" fallback={null}/>
  }
  if (event.type === 'issue.coding_session_archived') return <Archive size={14} />
  // A code host's own change (a closed pull request unassigned the agent): the host's mark.
  if (event.botActor?.type === 'integration') return <IntegrationBrandIcon provider={event.botActor.id === 'gitlab' ? 'gitlab' : 'github'} size={14}/>
  if (event.type.startsWith('attachment.') && event.members) return <Link2 size={14} />
  if (event.type.includes('relation')) {
    const type = event.entryType === 'blocks' || event.entryType === 'blocked_by' || event.entryType === 'duplicate-issue' ? event.entryType : event.metadata?.relationType || event.metadata?.type
    if ((phone || desktop) && type === 'blocks') return <span className="activity-blocking-icon"><IssueActionGlyph label="Blocking…" fallback={null}/></span>
    if (type === 'blocked_by') return <span className="activity-blocking-icon"><IssueActionGlyph label="Blocked by…" fallback={null}/></span>
    if (type === 'duplicate-issue') return <IssueActionGlyph label="Duplicate of…" fallback={null}/>
    return (phone || desktop) && (!type || type === 'related') ? <IssueActionGlyph label="Related to…" fallback={null} /> : <Link2 size={14} />
  }
  if (event.type.includes('review')) return <Link2 size={14} />
  const actor = activityActor(event.actor, event.botActor, context?.users)
  if (!event.actor?.id || actor.name === 'Flow' || SYSTEM_AUTOMATIONS.has(m.automation)) return <FlowLogo size={14}/>
  const avatar = <Avatar id={event.actor?.id} name={actor.name} avatarUrl={actor.avatarUrl} />
  const card = desktop && !event.botActor ? <PersonCardHover user={event.actor}>{avatar}</PersonCardHover> : avatar
  return phone ? <span className="activity-actor-avatar">{avatar}</span> : card
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
