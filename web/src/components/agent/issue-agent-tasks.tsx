import { useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import * as RadixMenu from '@radix-ui/react-dropdown-menu'
import { useCurrentTimeMs } from '@/hooks/use-current-time'
import { ArrowUp, CheckCircle2, Circle, CircleDot, ExternalLink, Paperclip, XCircle } from 'lucide-react'
import type { BootstrapData, Issue, User } from '@/types/flow'
import { UserAvatar } from '@/components/ui/user-avatar'
import { activityTimeLabel } from '@/components/activity/issue-activity-model'
import { commentShortcutMatches } from '@/lib/runtime-preferences'
import { AGENT_TASK_ACTIVITY_EVENT, archiveApplicationTask, deleteApplicationActivity, getApplicationTask, isApplicationTaskRunning, listApplicationTasks, reactApplicationTask, recommendedChoiceValue, replyApplicationTask, type ApplicationTask, type ApplicationActivity } from '@/lib/application-agents'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { EmojiPicker, ReactionPills } from '@/components/reactions/emoji-picker'
import { AgentAnswerText, AgentChoicePills } from './agent-answer'
import { AgentErrorDetail } from './agent-error-detail'
import { agentApprovalChoices, agentApprovalMarkdown } from './agent-approval-model'
import { AgentCommentGlyph } from './agent-comment-glyph'
import { agentThreadItems, agentThreadReactions, agentWorkingPhase } from './agent-thread-items'
import { CommentThreads, StandaloneSessions, TaskDirectory, type AgentThreadControls, type Directory, type StandaloneAgentSession } from './issue-agent-task-context'
import { FlowGlyph, type FlowGlyphName } from '@/components/ui/menu-glyphs'
import { toast } from '@/lib/toast'
import '@/components/project-detail/project-action-menu.css'
import '@/components/project-detail/project-comment-menu.css'
import { useI18n } from '@/i18n/i18n'
import './application-agents.css'
import './issue-agent-thread.css'
// coding sessions (track G)
import { AgentTaskCodingSession, CodingSessionActivityCard } from '@/components/coding-sessions/agent-task-coding-session'

type Resource = Pick<Issue, 'id' | 'delegate' | 'agentSessionId'>
type ResourceType = 'issue' | 'document' | 'project'

const taskFallbackPollMs = 10_000

function useTaskDirectory(issue: Resource, data: BootstrapData, resourceType: ResourceType, enabled: boolean): Directory {
  const [tasks, setTasks] = useState<ApplicationTask[]>([])
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const key = `${data.workspace.urlKey}:${resourceType}:${issue.id}`
  const available = Boolean(issue.delegate || data.users.some(user => user.app))
  useEffect(() => {
    if (!enabled || !available) return
    // Task changes arrive as pushes (agent_task.* realtime events, issue and comment updates). A slow fallback
    // poll runs only while a task is working, in case a push is missed; idle tasks are not polled.
    const abort = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    let loading = false
    let queued = false
    const load = async () => {
      clearTimeout(timer)
      timer = undefined
      if (loading) { queued = true; return }
      loading = true
      let running = false
      try {
        const next = await listApplicationTasks(data.workspace.urlKey, issue.id, abort.signal, resourceType)
        if (abort.signal.aborted) return
        setTasks(next); setError('')
        running = next.some(isApplicationTaskRunning)
      } catch (error) { if (!abort.signal.aborted) setError(error instanceof Error ? error.message : String(error)) }
      finally {
        loading = false
        if (!abort.signal.aborted && (queued || running)) timer = setTimeout(() => void load(), queued ? 0 : taskFallbackPollMs)
        queued = false
      }
    }
    const changed = (event: Event) => {
      const id = (event as CustomEvent<unknown>).detail
      if (!id || id === issue.id) void load()
    }
    window.addEventListener(AGENT_TASK_ACTIVITY_EVENT, changed)
    window.addEventListener('flow-issue-history-changed', changed)
    void load()
    return () => {
      abort.abort(); clearTimeout(timer)
      window.removeEventListener(AGENT_TASK_ACTIVITY_EVENT, changed)
      window.removeEventListener('flow-issue-history-changed', changed)
    }
  }, [enabled, available, data.workspace.urlKey, issue.id, issue.agentSessionId, resourceType, revision])
  return { key, workspace: data.workspace.urlKey, tasks, error, refresh: () => setRevision(value => value + 1) }
}

export function IssueAgentTasksProvider({ issue, data, resourceType = 'issue', children, onUpload }: { issue: Resource; data: BootstrapData; resourceType?: ResourceType; children: ReactNode; onUpload?: (file: File) => Promise<string> }) {
  const directory = useTaskDirectory(issue, data, resourceType, true)
  const idsKey = directory.tasks.map(task => task.commentId).filter(Boolean).sort().join(',')
  const ids = useMemo(() => new Set(idsKey ? idsKey.split(',') : []), [idsKey])
  const standaloneKey = byCreation(directory.tasks.filter(task => !task.commentId)).map(task => `${task.id}|${taskCreatedAt(task)}|${task.appUserId}`).join(',')
  const standalone = useMemo<StandaloneAgentSession[]>(() => standaloneKey ? standaloneKey.split(',').map(item => { const [id, createdAt, agentId] = item.split('|'); return { id, createdAt, agentId } }) : [], [standaloneKey])
  return <TaskDirectory.Provider value={{ ...directory, onUpload }}><CommentThreads.Provider value={ids}><StandaloneSessions.Provider value={standalone}>{children}</StandaloneSessions.Provider></CommentThreads.Provider></TaskDirectory.Provider>
}

const taskCreatedAt = (task: ApplicationTask) => task.createdAt || task.updatedAt
/** Sessions oldest first, like comments: a newer session never shows above an older one. */
function byCreation(tasks: ApplicationTask[]) {
  return [...tasks].sort((a, b) => (Date.parse(taskCreatedAt(a)) || 0) - (Date.parse(taskCreatedAt(b)) || 0) || a.id.localeCompare(b.id))
}

/** Where a thread's sessions render: an activity comment thread (issue, project, project update) or a document's comment card. */
export type AgentThreadVariant = 'thread' | 'document'

export function IssueAgentTasks({ issue, data, resourceType = 'issue', commentId, taskId, standalone = false, onUpload, threadControls, teamId, variant = 'thread' }: { issue: Resource; data: BootstrapData; resourceType?: ResourceType; /** The thread's comment, or all of its comments (a session started from a reply in the thread belongs to it too). */ commentId?: string | readonly string[]; /** Show only this session (the activity timeline places standalone sessions one by one). */ taskId?: string; standalone?: boolean; onUpload?: (file: File) => Promise<string>; /** The comment thread's own controls (subscription, resolve, sub-issues) for the agent replies' menu. */ threadControls?: AgentThreadControls; /** Team used by "New issue from comment". */ teamId?: string; variant?: AgentThreadVariant }) {
  const { t } = useI18n()
  const shared = useContext(TaskDirectory)
  const matches = shared?.key === `${data.workspace.urlKey}:${resourceType}:${issue.id}`
  const own = useTaskDirectory(issue, data, resourceType, !matches)
  const directory = matches ? shared : own
  const threadIds = commentId === undefined ? undefined : typeof commentId === 'string' ? [commentId] : commentId
  const tasks = byCreation(directory.tasks.filter(task => taskId ? task.id === taskId : threadIds ? Boolean(task.commentId && threadIds.includes(task.commentId)) : !standalone || !task.commentId))
  if (!tasks.length) return directory.error && !threadIds ? <div role="alert"><AgentErrorDetail error={directory.error} onRetry={directory.refresh}/></div> : null
  return <div className={`issue-agent-threads${threadIds ? ' is-comment-thread' : ' is-standalone'}${variant === 'document' ? ' is-document-card' : ''}`} aria-label={t('Agent sessions')}>
    {tasks.map(task => task.codingSessionId && !task.commentId
      // A delegated coding session is a session card (status row into the session view), not a message thread.
      ? <section key={task.id} className="issue-agent-thread is-coding-session" id={`agent-session-${task.id}`} data-agent-task-id={task.id}>
        <CodingSessionActivityCard task={task} data={data} agentName={data.users.find(user => user.id === task.appUserId)?.displayName || t('Flow')}/>
      </section>
      : <AgentTaskThread key={task.id} task={task} data={data} onChanged={directory.refresh} onUpload={onUpload ?? directory.onUpload} threadControls={threadControls} teamId={teamId} variant={variant}/>) }
  </div>
}

function AgentTaskThread({ task, data, onChanged, onUpload, threadControls, teamId, variant }: { task: ApplicationTask; data: BootstrapData; onChanged: () => void; onUpload?: (file: File) => Promise<string>; threadControls?: AgentThreadControls; teamId?: string; variant: AgentThreadVariant }) {
  const { t } = useI18n()
  const [activities, setActivities] = useState<ApplicationActivity[]>([])
  const [reply, setReply] = useState('')
  const [replying, setReplying] = useState(false)
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const fileInput = useRef<HTMLInputElement>(null)
  const [error, setError] = useState('')
  const [session, setSession] = useState(task)
  const [loadRevision, setLoadRevision] = useState(0)
  const [archived, setArchived] = useState(false)
  const cursor = useRef({ key: '', after: '' })
  const agent = data.users.find(user => user.id === task.appUserId)
  const name = agent?.displayName || agent?.name || t('Application')
  // Session fields that do not bump the version (plan, external URLs, derived stale/unresponsive) arrive with the
  // task list, which reloads on every push; a reply's own result is newer until the list catches up.
  const current = session.version > task.version ? session : task
  // AIG: an agent that never acknowledged the event is shown as unresponsive, and one silent for 30 minutes as stale,
  // instead of "Working…" forever.
  const unresponsive = Boolean(current.unresponsive) && ['pending', 'active'].includes(current.status)
  const generating = ['pending', 'active'].includes(current.status) && !unresponsive
  const items = useMemo(() => agentThreadItems(activities), [activities])
  const reactions = useMemo(() => agentThreadReactions(activities), [activities])
  const pendingQuestion = current.status === 'awaitingInput' ? items.findLast(item => item.activity.type === 'elicitation' && !item.resolved) : undefined
  const replyBody = reply.trim()
  useEffect(() => {
    const abort = new AbortController()
    const key = `${data.workspace.urlKey}:${task.id}`
    const load = async () => {
      const reset = cursor.current.key !== key
      let after = reset ? '' : cursor.current.after
      const next: ApplicationActivity[] = []
      let result
      do {
        result = await getApplicationTask(data.workspace.urlKey, task.id, after, abort.signal)
        next.push(...result.activities)
        if (result.activities.length) after = result.activities.at(-1)!.id
      } while (result.activities.length === 100)
      if (!abort.signal.aborted) {
        cursor.current = { key, after }
        setSession(result.session)
        setActivities(previous => [...new Map([...(reset ? [] : previous), ...next].map(activity => [activity.id, activity])).values()])
        setError('')
      }
    }
    void load().catch(error => { if (!abort.signal.aborted) setError(error instanceof Error ? error.message : String(error)) })
    return () => abort.abort()
  }, [data.workspace.urlKey, task.id, task.version, loadRevision])
  const submit = async (type: 'prompt' | 'canceled' | 'retry', body = reply, approve?: boolean) => {
    if (busyRef.current) return
    busyRef.current = true; setBusy(true); setError('')
    try {
      const updated = await replyApplicationTask(data.workspace.urlKey, current, type, body, approve)
      setSession(updated); setReply(''); setReplying(false); onChanged()
    } catch (error) { setError(error instanceof Error ? error.message : String(error)) }
    finally { busyRef.current = false; setBusy(false) }
  }
  const upload = async (file: File) => {
    if (!onUpload || busyRef.current) return
    busyRef.current = true; setBusy(true); setError('')
    try {
      const url = await onUpload(file)
      setReply(value => `${value}${value ? '\n' : ''}[${file.name.replace(/[[\]\\]/g, '')}](${url})`)
      setReplying(true)
    } catch (error) { setError(error instanceof Error ? error.message : String(error)) }
    finally { busyRef.current = false; setBusy(false) }
  }
  const react = async (activityId: string, emoji: string) => {
    if (busyRef.current || generating) return
    busyRef.current = true; setBusy(true); setError('')
    const enabled = !reactions[activityId]?.[emoji]?.includes(data.viewer.id)
    try {
      const updated = await reactApplicationTask(data.workspace.urlKey, current, activityId, emoji, enabled)
      setSession(updated)
      setActivities(previous => [...previous, { id: String(updated.version).padStart(20, '0'), sessionId: task.id, actorId: data.viewer.id, type: 'feedback', body: emoji, metadata: { activityId, enabled }, createdAt: updated.updatedAt }])
      onChanged()
    } catch (error) { setError(error instanceof Error ? error.message : String(error)); onChanged() }
    finally { busyRef.current = false; setBusy(false) }
  }
  const remove = async (activityId: string) => {
    if (busyRef.current) return
    busyRef.current = true; setBusy(true); setError('')
    try {
      const updated = await deleteApplicationActivity(data.workspace.urlKey, current, activityId)
      setSession(updated)
      setActivities(previous => [...previous, { id: String(updated.version).padStart(20, '0'), sessionId: task.id, actorId: data.viewer.id, type: 'deleted', body: '', metadata: { activityId }, createdAt: updated.updatedAt }])
      onChanged()
    } catch (error) { setError(error instanceof Error ? error.message : String(error)) }
    finally { busyRef.current = false; setBusy(false) }
  }
  // Like the reference app, a comment thread never shows the agent's thinking or steps: while it works the thread ends with one
  // "<Agent> Working…" row (the current phase, shimmering), and its answer then lands as a reply.
  const shown = items.filter(item => item.kind === 'message' && !(generating && item.output))
  const phase = generating ? agentWorkingPhase(activities, t) : ''
  const canDelete = data.viewer.id === task.creatorId || data.viewerRole === 'admin' || data.viewerRole === 'owner'
  // The whole session (every message in it, people's replies included) can be archived by whoever started it or an
  // admin, as the server enforces; deleting a reply hides only that reply.
  const canArchive = !data.viewer.app && canDelete
  const archiveSession = async () => {
    if (busyRef.current) return
    const description = [
      t('The session and all of its messages, including the replies people sent the agent, will be hidden from this activity for everyone. A running session is stopped first.'),
      t('The execution log, the audit log, reports and documents it created, attachments and linked issues are kept. To remove a single reply, use Delete on that reply instead.'),
      ...(task.commentId ? [t('The comment that mentioned the agent stays.')] : []),
    ].join(' ')
    if (!await confirmAction(t('Archive this agent session?'), { description, confirmLabel: t('Archive session'), cancelLabel: t('Cancel'), danger: true })) return
    busyRef.current = true; setBusy(true); setError('')
    try {
      await archiveApplicationTask(data.workspace.urlKey, task.id)
      setArchived(true)
      toast.success(t('Agent session archived'))
      onChanged()
    } catch (error) { setError(error instanceof Error ? error.message : String(error)) }
    finally { busyRef.current = false; setBusy(false) }
  }
  const sessionMenu = canArchive ? <AgentSessionMenu linkId={`agent-session-${task.id}`} onArchive={() => void archiveSession()}/> : null
  // Gone at once for the archiver; other viewers drop it on the agent_task push (the list leaves archived sessions out).
  if (archived || current.archivedAt) return null
  const delegator = task.trigger === 'delegation' && !task.commentId ? data.users.find(user => user.id === task.creatorId) : undefined
  // A document's comment card says "Reply…", like its own reply row.
  const replyPlaceholder = t(variant === 'document' ? 'Reply…' : 'Leave a reply…')
  return <section className="issue-agent-thread" id={`agent-session-${task.id}`} data-agent-task-id={task.id} data-status={current.status} data-unresponsive={unresponsive || undefined} aria-label={`${name} ${t('Agent session')}`}>
    {/* AIG: a delegated issue names the responsible human ("Delegated to Flow by Skyler"). */}
    {delegator && <div className="issue-agent-delegation"><AgentMessageHeader user={agent} name={t('Delegated to {name} by {user}').replace('{name}', name).replace('{user}', delegator.displayName || delegator.name)} time={taskCreatedAt(task)}/>{sessionMenu && <div className="issue-agent-message-actions">{sessionMenu}</div>}</div>}
    <AgentSessionDetails session={current}/>
    {/* coding sessions (track G): the session view when this task anchors a coding session */}
    <AgentTaskCodingSession task={current} data={data}/>
    {shown.map(item => {
      const activity = item.activity
      const human = ['prompt', 'retry', 'canceled'].includes(activity.type)
      const user = human ? data.users.find(user => user.id === activity.actorId) || data.viewer : agent
      const approval = activity.type === 'elicitation' ? activity.signalMetadata?.approval : undefined
      const options = (activity.signal === 'select' ? activity.signalMetadata?.options ?? [] : []).map(option => ({ label: option.label || option.value, value: option.value }))
      // An approval (bulk delete, an update the agent wrote) reads as a preview in the viewer's language with Approve / Reject.
      const choices = approval ? agentApprovalChoices(options, t) : options
      const body = approval && approval.kind !== 'connector' ? agentApprovalMarkdown(approval, t) : item.body
      const auth = activity.signal === 'auth' && !item.resolved
      const authUser = activity.signalMetadata?.userId
      return <article className="issue-agent-message" key={item.id} id={`agent-activity-${item.id}`} data-type={activity.type} data-message-id={item.id}>
        <AgentMessageHeader user={user} name={human ? user?.displayName || t('User') : name} time={activity.createdAt} anchor={`agent-activity-${item.id}`}/>
        {!human && !item.output && <div className="issue-agent-message-actions">
          <EmojiPicker align="end" onSelect={emoji => react(activity.id, emoji)}><button type="button" aria-label={t('Add reaction')} disabled={busy || generating}><AgentCommentGlyph name="reaction"/></button></EmojiPicker>
          {variant !== 'document' && <span className="issue-agent-votes">{(['👍', '👎'] as const).map(emoji => <button key={emoji} type="button" aria-label={`${t(emoji === '👍' ? 'React with thumbs up' : 'React with thumbs down')}. ${t('Reaction count')}: ${reactions[activity.id]?.[emoji]?.length ?? 0}.`} aria-pressed={reactions[activity.id]?.[emoji]?.includes(data.viewer.id) ?? false} disabled={busy || generating} onClick={() => void react(activity.id, emoji)}><AgentCommentGlyph name={emoji === '👍' ? 'up' : 'down'}/></button>)}</span>}
          <AgentReplyCommentMenu
            body={item.body}
            controls={threadControls}
            linkId={`agent-activity-${item.id}`}
            onDelete={canDelete ? () => void remove(activity.id) : undefined}
            onArchiveSession={canArchive ? () => void archiveSession() : undefined}
            teamId={teamId}
          />
        </div>}
        {/* A person's reply in the session has no reply menu of its own; it offers the session's menu. */}
        {human && sessionMenu && !delegator && <div className="issue-agent-message-actions">{sessionMenu}</div>}
        <div className="issue-agent-message-body">{activity.type === 'error' ? <AgentErrorDetail error={body} onRetry={!busy && !generating ? () => void submit('retry') : undefined}/> : <AgentAnswerText className="issue-agent-markdown" data={data} markdown={body}/>}
          {choices.length > 0 && <AgentChoicePills choices={choices} disabled={busy || item.id !== pendingQuestion?.id} onSelect={choice => void submit('prompt', choice.value)} recommended={approval || activity.signal !== 'select' ? undefined : recommendedChoiceValue(activity.signalMetadata)} selected={item.resolved ? item.selected ?? '' : undefined}/>}
          {auth && (authUser && authUser !== data.viewer.id ? <p className="issue-agent-auth-wait">{t('Waiting for')} {data.users.find(user => user.id === authUser)?.displayName || t('another member')} {t('to link their account.')}</p> : <a className="issue-agent-auth-link" href={activity.signalMetadata?.url} target="_blank" rel="noreferrer"><ExternalLink size={14}/>{t('Link account')}</a>)}
          {activity.url && <a href={activity.url} target="_blank" rel="noreferrer">{t('Open result')}</a>}
          {!human && <ReactionPills reactions={reactions[activity.id]} viewerId={data.viewer.id} onToggle={emoji => react(activity.id, emoji)}/>}
        </div>
      </article>
    })}
    {generating && <div className="issue-agent-working" role="status" aria-live="polite">
      <AgentMessageHeader user={agent} name={name} time=""/>
      <AgentWorkingLabel text={phase}/>
      {sessionMenu && !delegator && <div className="issue-agent-message-actions">{sessionMenu}</div>}
    </div>}
    {(unresponsive || current.status === 'stale') && <div className="issue-agent-working issue-agent-session-state" role="status" data-state={unresponsive ? 'unresponsive' : 'stale'}>
      <AgentMessageHeader user={agent} name={name} time=""/>
      <span className="issue-agent-working-text">{t(unresponsive ? '{name} didn’t respond' : 'No activity from {name} for 30 minutes').replace('{name}', name)}</span>
      {sessionMenu && !delegator && <div className="issue-agent-message-actions">{sessionMenu}</div>}
    </div>}
    {current.pendingTool && current.status === 'awaitingInput' && !pendingQuestion?.activity.signalMetadata?.options?.length && <div className="issue-agent-approval"><span>{t('Approval required')}: {current.pendingTool.name}</span><button type="button" disabled={busy} onClick={() => void submit('prompt', '', false)}>{t('Reject')}</button><button type="button" disabled={busy} onClick={() => void submit('prompt', '', true)}>{t('Approve')}</button></div>}
    <div className="issue-agent-thread-footer">
      {onUpload && (
        <input ref={fileInput} type="file" hidden onChange={event => {
          const file = event.target.files?.[0]
          event.target.value = ''
          if (file) void upload(file)
        }}/>
      )}
      {replying || reply ? (
        <form onSubmit={event => { event.preventDefault(); if (replyBody) void submit('prompt', replyBody) }} data-tooltip-elevated="">
          <div className="issue-agent-reply-input">
            <UserAvatar name={data.viewer.displayName} avatarUrl={data.viewer.avatarUrl} className="avatar"/>
            <textarea autoFocus={replying} aria-label={t('Reply to agent')} placeholder={replyPlaceholder} value={reply} disabled={busy} onChange={event => setReply(event.target.value)} onKeyDown={event => {
              if (event.key === 'Escape' && !reply.trim()) setReplying(false)
              if (commentShortcutMatches(event) && replyBody) { event.preventDefault(); void submit('prompt', replyBody) }
            }}/>
          </div>
          <div className="issue-agent-reply-tools">
            <span/>
            {onUpload && <button type="button" aria-label={t('Attach images, files, or videos')} disabled={busy} onClick={() => fileInput.current?.click()}><Paperclip size={14}/></button>}
            <button type="submit" aria-label={t('Send reply')} disabled={busy || !replyBody}><ArrowUp size={14}/></button>
          </div>
        </form>
      ) : <>
        <button type="button" className="issue-agent-start-reply" onClick={() => setReplying(true)}><UserAvatar name={data.viewer.displayName} avatarUrl={data.viewer.avatarUrl} className="avatar"/><span>{replyPlaceholder}</span></button>
        <div className="issue-agent-idle-tools">
          {/* A session with no row to carry its menu (stopped before the agent replied) offers it here. */}
          {sessionMenu && !delegator && !shown.length && !generating && !unresponsive && current.status !== 'stale' && sessionMenu}
          {onUpload &&<button type="button" aria-label={t('Attach images, files, or videos')} disabled={busy} onClick={() => fileInput.current?.click()}><Paperclip size={14}/></button>}
          <button type="button" aria-label={t('Send reply')} disabled><ArrowUp size={14}/></button>
        </div>
      </>}
    </div>
    {error && <div className="issue-agent-thread-error" role="alert"><AgentErrorDetail error={error} onRetry={() => setLoadRevision(value => value + 1)}/></div>}
  </section>
}

/**
 * What an agent attached to its session (the reference app's agentSessionUpdate): external links as chips, such as its pull request
 * or run page, and its plan as a checklist (pending, in progress, completed, canceled) above the replies.
 */
export function AgentSessionDetails({ session }: { session: Pick<ApplicationTask, 'externalUrls' | 'plan'> }) {
  const { t } = useI18n()
  const links = (session.externalUrls ?? []).filter(link => /^https?:\/\//i.test(link.url))
  const plan = session.plan ?? []
  if (!links.length && !plan.length) return null
  const label = (link: { url: string; label?: string }) => {
    if (link.label?.trim()) return link.label.trim()
    try { return new URL(link.url).hostname } catch { return link.url }
  }
  const stepLabel = { pending: 'Pending', inProgress: 'In progress', completed: 'Completed', canceled: 'Canceled' } as const
  return <div className="issue-agent-session-details">
    {links.length > 0 && <div className="issue-agent-external-links" aria-label={t('Session links')}>
      {links.map(link => <a key={link.url} href={link.url} target="_blank" rel="noreferrer" title={link.url}><ExternalLink size={12} aria-hidden="true"/><span data-i18n-ignore>{label(link)}</span></a>)}
    </div>}
    {plan.length > 0 && <ol className="issue-agent-plan" aria-label={t('Plan')}>
      {plan.map((step, index) => {
        const Icon = step.status === 'completed' ? CheckCircle2 : step.status === 'canceled' ? XCircle : step.status === 'inProgress' ? CircleDot : Circle
        return <li key={index} data-status={step.status}><Icon size={14} aria-label={t(stepLabel[step.status] ?? 'Pending')}/><span data-i18n-ignore>{step.content}</span></li>
      })}
    </ol>}
  </div>
}

/** The working row's label: muted text with the reference app's light sweep, fading in whenever the phase changes. */
function AgentWorkingLabel({ text }: { text: string }) {
  return <span className="issue-agent-working-label" key={text}>
    <span className="issue-agent-working-text">{text}<span aria-hidden="true" className="issue-agent-working-shine">{text}</span></span>
  </span>
}

/**
 * The "…" menu on an agent's reply in a comment thread, as the reference app lists it: thread subscription, resolve the thread,
 * copy link / Markdown, new issue or sub-issue from the reply, delete.
 */
function AgentReplyCommentMenu({ body, controls, linkId, onDelete, onArchiveSession, teamId }: { body: string; controls?: AgentThreadControls; linkId: string; onDelete?: () => void; /** Archives the whole session this reply belongs to. */ onArchiveSession?: () => void; teamId?: string }) {
  const { t } = useI18n()
  const copy = (value: string, done: string) => void navigator.clipboard.writeText(value)
    .then(() => toast.success(t(done)))
    .catch(() => toast.error(t('Could not copy to clipboard')))
  const newIssue = (subIssue: boolean) => window.dispatchEvent(new CustomEvent('flow-create-issue-from-selection', { detail: { text: body, teamId, parentId: subIssue ? controls?.parentIssueId : undefined } }))
  const afterClose = useAfterMenuClose()
  return <RadixMenu.Root>
    <RadixMenu.Trigger asChild><button type="button" aria-label={t('Comment options')}><AgentCommentGlyph name="more"/></button></RadixMenu.Trigger>
    <RadixMenu.Portal>
      <RadixMenu.Content data-flow-motion="floating" align="end" sideOffset={4} collisionPadding={16} className="project-action-menu project-comment-menu" aria-label={t('Comment options')} onCloseAutoFocus={afterClose.onCloseAutoFocus}>
        {controls?.onToggleSubscription && <><MenuItem glyph={controls.following ? 'unsubscribe' : 'subscribe'} label={t(controls.following ? 'Unsubscribe from thread' : 'Subscribe to thread')} onSelect={controls.onToggleSubscription}/><RadixMenu.Separator/></>}
        {controls?.onResolve && <><MenuItem glyph="resolveThread" label={t(controls.resolved ? 'Unresolve thread' : 'Resolve thread with comment')} onSelect={controls.onResolve}/><RadixMenu.Separator/></>}
        <MenuItem glyph="copyLink" label={t('Copy link to comment')} onSelect={() => copy(`${location.href.split('#')[0]}#${linkId}`, 'Copied link to comment')}/>
        <MenuItem glyph="copyMarkdown" label={t('Copy content as Markdown')} onSelect={() => copy(body, 'Copied to clipboard')}/>
        <RadixMenu.Separator/>
        <MenuItem glyph="newIssueFromComment" label={t('New issue from comment…')} onSelect={() => newIssue(false)}/>
        {controls?.parentIssueId && <MenuItem glyph="newSubIssueFromComment" label={t('New sub-issue from comment…')} onSelect={() => newIssue(true)}/>}
        {onDelete && <><RadixMenu.Separator/><MenuItem glyph="delete" label={t('Delete')} onSelect={onDelete}/></>}
        {onArchiveSession && <>{!onDelete && <RadixMenu.Separator/>}<MenuItem glyph="agentSessionDismissed" label={t('Archive session…')} onSelect={() => afterClose.then(onArchiveSession)}/></>}
      </RadixMenu.Content>
    </RadixMenu.Portal>
  </RadixMenu.Root>
}

/**
 * Runs a menu action once the menu has closed, without handing focus back to its trigger: the confirmation dialog
 * the action opens would otherwise see that focus move as an outside interaction and close at once.
 */
function useAfterMenuClose() {
  const pending = useRef<(() => void) | undefined>(undefined)
  return {
    then: (action: () => void) => { pending.current = action },
    onCloseAutoFocus: (event: Event) => {
      const action = pending.current
      pending.current = undefined
      if (!action) return
      event.preventDefault()
      action()
    },
  }
}

/** The session's own "…" menu (delegation header, people's replies, the working row): its link and "Archive session…". */
function AgentSessionMenu({ linkId, onArchive }: { linkId: string; onArchive: () => void }) {
  const { t } = useI18n()
  const copyLink = () => void navigator.clipboard.writeText(`${location.href.split('#')[0]}#${linkId}`)
    .then(() => toast.success(t('Link copied to clipboard')))
    .catch(() => toast.error(t('Could not copy to clipboard')))
  const afterClose = useAfterMenuClose()
  return <RadixMenu.Root>
    <RadixMenu.Trigger asChild><button type="button" aria-label={t('Agent session options')}><AgentCommentGlyph name="more"/></button></RadixMenu.Trigger>
    <RadixMenu.Portal>
      <RadixMenu.Content data-flow-motion="floating" align="end" sideOffset={4} collisionPadding={16} className="project-action-menu project-comment-menu" aria-label={t('Agent session options')} onCloseAutoFocus={afterClose.onCloseAutoFocus}>
        <MenuItem glyph="copyLink" label={t('Copy link to session')} onSelect={copyLink}/>
        <RadixMenu.Separator/>
        <MenuItem glyph="agentSessionDismissed" label={t('Archive session…')} onSelect={() => afterClose.then(onArchive)}/>
      </RadixMenu.Content>
    </RadixMenu.Portal>
  </RadixMenu.Root>
}

function MenuItem({ glyph, label, onSelect }: { glyph: FlowGlyphName; label: string; onSelect: () => void }) {
  return <RadixMenu.Item onSelect={onSelect}><span className="project-menu-icon" aria-hidden="true"><FlowGlyph name={glyph}/></span><span className="project-menu-label">{label}</span></RadixMenu.Item>
}

function AgentMessageHeader({ user, name, time, anchor }: { user?: User; name: string; time: string; anchor?: string }) {
  const { locale } = useI18n()
  const now = useCurrentTimeMs()
  const label = time && !Number.isNaN(Date.parse(time)) ? activityTimeLabel(time, now, locale).replace(/m(?= ago$)/, 'min') : ''
  return <header className="issue-agent-message-header"><UserAvatar className="avatar" name={name} avatarUrl={user?.avatarUrl}/><strong data-i18n-ignore>{name}</strong>{label && (anchor ? <a href={`#${anchor}`}><time dateTime={time}>{label}</time></a> : <time dateTime={time}>{label}</time>)}</header>
}
