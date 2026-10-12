import { useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { ArrowUp, Check, ChevronDown, ChevronRight, Copy, ExternalLink, Paperclip, RefreshCw, Square } from 'lucide-react'
import { GridLoader } from '@/components/ui/grid-loader'
import type { BootstrapData, Issue, IssueUpdateInput, User } from '@/types/flow'
import { PersonPicker } from '@/components/issue/core-property-pickers'
import { UserAvatar } from '@/components/ui/user-avatar'
import { activityTimeLabel } from '@/components/activity/issue-activity-model'
import { commentShortcutMatches } from '@/lib/runtime-preferences'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { AGENT_TASK_ACTIVITY_EVENT, canDelegateTo, getApplicationTask, isApplicationTaskRunning, listApplicationTasks, reactApplicationTask, replyApplicationTask, type ApplicationTask, type ApplicationActivity } from '@/lib/application-agents'
import { EmojiPicker, ReactionPills } from '@/components/reactions/emoji-picker'
import { AgentAnswerText } from './agent-answer'
import { AgentCommentGlyph } from './agent-comment-glyph'
import { agentThreadItems, agentThreadReactions } from './agent-thread-items'
import { CommentThreads, TaskDirectory, type Directory } from './issue-agent-task-context'
import { useI18n } from '@/i18n/i18n'
import './application-agents.css'
import './issue-agent-thread.css'

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
  return { key, tasks, error, refresh: () => setRevision(value => value + 1) }
}

export function IssueAgentTasksProvider({ issue, data, resourceType = 'issue', children, onUpload }: { issue: Resource; data: BootstrapData; resourceType?: ResourceType; children: ReactNode; onUpload?: (file: File) => Promise<string> }) {
  const directory = useTaskDirectory(issue, data, resourceType, true)
  const idsKey = directory.tasks.map(task => task.commentId).filter(Boolean).sort().join(',')
  const ids = useMemo(() => new Set(idsKey ? idsKey.split(',') : []), [idsKey])
  return <TaskDirectory.Provider value={{ ...directory, onUpload }}><CommentThreads.Provider value={ids}>{children}</CommentThreads.Provider></TaskDirectory.Provider>
}

export function IssueAgentPicker({ issue, data, onUpdate }: { issue: Issue; data: BootstrapData; onUpdate: (input: IssueUpdateInput) => Promise<void> }) {
  const { t } = useI18n()
  const users = data.users.filter(user => canDelegateTo(user, issue.team.id))
  if (!users.length && !issue.delegate || data.viewerRole === 'guest' && data.workspaceSettings.preventGuestAgents) return null
  return <div className="core-property-picker"><PersonPicker label="Agent" ariaLabel={t('Delegate to agent')} people={users.map(user => ({ ...user, label: user.displayName }))} selectedId={issue.delegate?.id} onChange={delegateId => onUpdate({ delegateId })} emptyOptionLabel="No agent" emptyTriggerLabel="Delegate to agent" searchPlaceholder="Find agent…" triggerClassName="core-property-trigger"/></div>
}

export function IssueAgentTasks({ issue, data, resourceType = 'issue', commentId, standalone = false, onUpload }: { issue: Resource; data: BootstrapData; resourceType?: ResourceType; commentId?: string; standalone?: boolean; onUpload?: (file: File) => Promise<string> }) {
  const { t } = useI18n()
  const shared = useContext(TaskDirectory)
  const matches = shared?.key === `${data.workspace.urlKey}:${resourceType}:${issue.id}`
  const own = useTaskDirectory(issue, data, resourceType, !matches)
  const directory = matches ? shared : own
  const tasks = directory.tasks.filter(task => commentId ? task.commentId === commentId : !standalone || !task.commentId)
  if (!tasks.length) return directory.error && !commentId ? <p role="alert">{directory.error}</p> : null
  return <div className={`issue-agent-threads${commentId ? ' is-comment-thread' : ' is-standalone'}`} aria-label={t('Agent sessions')}>
    {tasks.map(task => <AgentTaskThread key={task.id} task={task} data={data} onChanged={directory.refresh} onUpload={onUpload ?? directory.onUpload}/>) }
  </div>
}

function AgentTaskThread({ task, data, onChanged, onUpload }: { task: ApplicationTask; data: BootstrapData; onChanged: () => void; onUpload?: (file: File) => Promise<string> }) {
  const { t } = useI18n()
  const [activities, setActivities] = useState<ApplicationActivity[]>([])
  const [reply, setReply] = useState('')
  const [replying, setReplying] = useState(false)
  const [selection, setSelection] = useState('')
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const fileInput = useRef<HTMLInputElement>(null)
  const [error, setError] = useState('')
  const [session, setSession] = useState(task)
  const cursor = useRef({ key: '', after: '' })
  const agent = data.users.find(user => user.id === task.appUserId)
  const name = agent?.displayName || agent?.name || t('Application')
  const current = session.version >= task.version ? session : task
  const generating = ['pending', 'active'].includes(current.status)
  const items = useMemo(() => agentThreadItems(activities), [activities])
  const reactions = useMemo(() => agentThreadReactions(activities), [activities])
  const thinkingId = generating ? items.findLast(item => item.kind === 'thinking')?.id : undefined
  const pendingQuestion = current.status === 'awaitingInput' ? items.findLast(item => item.activity.type === 'elicitation' && !item.resolved) : undefined
  const options = pendingQuestion?.activity.signal === 'select' ? pendingQuestion.activity.signalMetadata?.options ?? [] : []
  const selectedOption = options.find(option => option.value === selection)
  const replyBody = selectedOption ? `${selectedOption.value}${reply.trim() ? `\n\n${reply.trim()}` : ''}` : reply.trim()
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
  }, [data.workspace.urlKey, task.id, task.version])
  const submit = async (type: 'prompt' | 'canceled' | 'retry', body = reply, approve?: boolean) => {
    if (busyRef.current) return
    busyRef.current = true; setBusy(true); setError('')
    try {
      const updated = await replyApplicationTask(data.workspace.urlKey, current, type, body, approve)
      setSession(updated); setReply(''); setSelection(''); setReplying(false); onChanged()
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
  return <section className="issue-agent-thread" data-agent-task-id={task.id} data-status={current.status} aria-label={`${name} ${t('Agent session')}`}>
    {items.map(item => {
      const activity = item.activity
      const human = ['prompt', 'retry', 'canceled'].includes(activity.type)
      const user = human ? data.users.find(user => user.id === activity.actorId) || data.viewer : agent
      if (item.kind === 'thinking') return <details className="issue-agent-thinking" key={item.id}><summary><ChevronRight size={12}/><span>{t(item.id === thinkingId ? 'Thinking…' : 'Thought process')}</span>{item.id === thinkingId && <GridLoader className="issue-agent-spinner" size={12}/>}</summary><AgentAnswerText className="issue-agent-markdown" data={data} markdown={item.body}/></details>
      if (item.kind === 'action') return <div className="issue-agent-action" key={item.id}>{activity.type === 'canceled' ? <Square size={12}/> : <Check size={12}/>}<span>{activity.metadata?.action || item.body || t('Action')}</span>{activity.metadata?.parameter && <small>{activity.metadata.parameter}</small>}{activity.metadata?.result && <AgentAnswerText className="issue-agent-markdown" data={data} markdown={activity.metadata.result}/>}</div>
      const options = activity.signal === 'select' ? activity.signalMetadata?.options ?? [] : []
      const selected = options.find(option => option.value === item.selected)
      const visibleOptions = item.resolved && selected ? [selected] : []
      const auth = activity.signal === 'auth' && !item.resolved
      const authUser = activity.signalMetadata?.userId
      return <article className="issue-agent-message" key={item.id} data-type={activity.type} data-message-id={item.id}>
        <AgentMessageHeader user={user} name={human ? user?.displayName || t('User') : name} time={activity.createdAt}/>
        {!human && !item.output && <div className="issue-agent-message-actions">
          <EmojiPicker align="end" onSelect={emoji => react(activity.id, emoji)}><button type="button" aria-label={t('Add reaction')} disabled={busy || generating}><AgentCommentGlyph name="reaction"/></button></EmojiPicker>
          <span className="issue-agent-votes">{(['👍', '👎'] as const).map(emoji => <button key={emoji} type="button" aria-label={`${t(emoji === '👍' ? 'React with thumbs up' : 'React with thumbs down')}. ${t('Reaction count')}: ${reactions[activity.id]?.[emoji]?.length ?? 0}.`} aria-pressed={reactions[activity.id]?.[emoji]?.includes(data.viewer.id) ?? false} disabled={busy || generating} onClick={() => void react(activity.id, emoji)}><AgentCommentGlyph name={emoji === '👍' ? 'up' : 'down'}/></button>)}</span>
          <DropdownMenu><DropdownMenuTrigger asChild><button type="button" aria-label={t('Open menu')}><AgentCommentGlyph name="more"/></button></DropdownMenuTrigger><DropdownMenuContent align="end"><DropdownMenuItem onSelect={() => { void navigator.clipboard.writeText(item.body).catch(error => setError(String(error))) }}><Copy size={14}/>{t('Copy response')}</DropdownMenuItem><DropdownMenuItem disabled={busy} onSelect={() => void submit(generating || current.status === 'awaitingInput' ? 'canceled' : 'retry', '')}>{generating || current.status === 'awaitingInput' ? <><Square size={14}/>{t('Send stop request')}</> : <><RefreshCw size={14}/>{t('Retry agent')}</>}</DropdownMenuItem></DropdownMenuContent></DropdownMenu>
        </div>}
        <div className="issue-agent-message-body"><AgentAnswerText className="issue-agent-markdown" data={data} markdown={item.body}/>
          {visibleOptions.length > 0 && <div className="issue-agent-choices is-resolved" role="group" aria-label={t('Selected option')}>{visibleOptions.map(option => <button key={option.value} type="button" aria-pressed disabled><span>{option.label || option.value}</span><Check size={14}/></button>)}</div>}
          {auth && (authUser && authUser !== data.viewer.id ? <p className="issue-agent-auth-wait">{t('Waiting for')} {data.users.find(user => user.id === authUser)?.displayName || t('another member')} {t('to link their account.')}</p> : <a className="issue-agent-auth-link" href={activity.signalMetadata?.url} target="_blank" rel="noreferrer"><ExternalLink size={14}/>{t('Link account')}</a>)}
          {activity.url && <a href={activity.url} target="_blank" rel="noreferrer">{t('Open result')}</a>}
          {!human && <ReactionPills reactions={reactions[activity.id]} viewerId={data.viewer.id} onToggle={emoji => react(activity.id, emoji)}/>}
        </div>
      </article>
    })}
    {generating && !items.some(item => item.output) && <div className="issue-agent-working" role="status"><GridLoader className="issue-agent-spinner" size={13}/><span>{t(current.status === 'pending' ? 'Thinking…' : 'Working…')}</span></div>}
    {current.pendingTool && current.status === 'awaitingInput' && <div className="issue-agent-approval"><span>{t('Approval required')}: {current.pendingTool.name}</span><button type="button" disabled={busy} onClick={() => void submit('prompt', '', false)}>{t('Reject')}</button><button type="button" disabled={busy} onClick={() => void submit('prompt', '', true)}>{t('Approve')}</button></div>}
    <div className="issue-agent-thread-footer">
      {onUpload && (
        <input ref={fileInput} type="file" hidden onChange={event => {
          const file = event.target.files?.[0]
          event.target.value = ''
          if (file) void upload(file)
        }}/>
      )}
      {replying || reply || options.length > 0 ? (
        <form onSubmit={event => { event.preventDefault(); if (replyBody) void submit('prompt', replyBody) }}>
          <div className="issue-agent-reply-input">
            <UserAvatar name={data.viewer.displayName} avatarUrl={data.viewer.avatarUrl} className="avatar"/>
            <textarea autoFocus={replying} aria-label={t('Reply to agent')} placeholder={t('Leave a reply…')} value={reply} disabled={busy} onChange={event => setReply(event.target.value)} onKeyDown={event => {
              if (event.key === 'Escape' && !reply.trim()) setReplying(false)
              if (commentShortcutMatches(event) && replyBody) { event.preventDefault(); void submit('prompt', replyBody) }
            }}/>
          </div>
          <div className="issue-agent-reply-tools">
            <AgentChoices options={options} selected={selectedOption?.value} disabled={busy} onSelect={value => setSelection(selection === value ? '' : value)}/>
            {onUpload && <button type="button" aria-label={t('Attach images, files, or videos')} disabled={busy} onClick={() => fileInput.current?.click()}><Paperclip size={14}/></button>}
            {generating && !replyBody ? <button type="button" aria-label={t('Stop agent')} disabled={busy} onClick={() => void submit('canceled', '')}><Square size={14}/></button> : (
              <button type="submit" className={selectedOption ? 'has-label' : ''} aria-label={t(selectedOption ? 'Continue with selected option' : 'Send reply')} disabled={busy || !replyBody}>
                {selectedOption && <span>{t('Continue')}</span>}<ArrowUp size={14}/>
              </button>
            )}
          </div>
        </form>
      ) : <>
        <button type="button" className="issue-agent-start-reply" onClick={() => setReplying(true)}><UserAvatar name={data.viewer.displayName} avatarUrl={data.viewer.avatarUrl} className="avatar"/><span>{t('Leave a reply…')}</span></button>
        <div className="issue-agent-idle-tools">
          {onUpload && <button type="button" aria-label={t('Attach images, files, or videos')} disabled={busy} onClick={() => fileInput.current?.click()}><Paperclip size={14}/></button>}
          <button type="button" aria-label={t(generating ? 'Stop agent' : 'Send reply')} disabled={busy || !generating} onClick={() => void submit('canceled', '')}>{generating ? <Square size={14}/> : <ArrowUp size={14}/>}</button>
        </div>
      </>}
    </div>
    {error && <p className="issue-agent-thread-error" role="alert">{error}</p>}
  </section>
}

function AgentChoices({ options, selected, disabled, onSelect }: { options: { label?: string; value: string }[]; selected?: string; disabled: boolean; onSelect: (value: string) => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  if (!options.length) return <span/>
  const selectedOption = options.find(option => option.value === selected)
  const inline = options.length === 1 || options.length <= 2 && options.every(option => (option.label || option.value).length < 30 && !/^https?:\/\//.test(option.value))
  return <div className="issue-agent-choices" role="group" aria-label={t('Select an option')}>{inline ? options.map(option => <button key={option.value} type="button" title={option.label && option.label !== option.value ? `${option.label}\n${option.value}` : undefined} aria-pressed={option.value === selected} disabled={disabled} onClick={() => onSelect(option.value)}><span>{option.label || option.value}</span></button>) : <DropdownMenu onOpenChange={() => setQuery('')}><DropdownMenuTrigger asChild><button type="button" disabled={disabled} aria-label={t('Select an option')}><span>{selectedOption?.label || selectedOption?.value || t('Select an option')}</span><ChevronDown size={14}/></button></DropdownMenuTrigger><DropdownMenuContent><input autoFocus aria-label={t('Search options')} placeholder={t('Search options…')} value={query} onChange={event => setQuery(event.target.value)} onKeyDown={event => event.stopPropagation()}/>{options.filter(option => `${option.label || ''} ${option.value}`.toLowerCase().includes(query.toLowerCase())).map(option => <DropdownMenuItem key={option.value} onSelect={() => onSelect(option.value)}>{option.label || option.value}{option.value === selected && <Check size={14}/>}</DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu>}</div>
}

function AgentMessageHeader({ user, name, time }: { user?: User; name: string; time: string }) {
  const { locale } = useI18n()
  return <header className="issue-agent-message-header"><UserAvatar className="avatar" name={name} avatarUrl={user?.avatarUrl}/><strong data-i18n-ignore>{name}</strong>{time && !Number.isNaN(Date.parse(time)) && <time dateTime={time}>{activityTimeLabel(time, Date.now(), locale).replace(/m(?= ago$)/, 'min')}</time>}</header>
}
