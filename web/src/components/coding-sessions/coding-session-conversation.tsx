import { Fragment, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react'
import { ArrowUpRight, ChevronDown } from 'lucide-react'
import { approveCodingSession, promptCodingSession, restartCodingSession, stopCodingSession } from '@/lib/coding-sessions-api'
import { AGENT_TASK_ACTIVITY_EVENT, getApplicationTask, type ApplicationActivity } from '@/lib/application-agents'
import { AgentAnswerText } from '@/components/agent/agent-answer'
import { AgentCopyGlyph } from '@/components/ui/agent-glyph'
import { AppLink } from '@/components/ui/app-link'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import { issuePath } from '@/lib/app-routes'
import { toast } from '@/lib/toast'
import type { BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { CodingComposer, type CodingComposerProps, type ComposerRequest } from './coding-session-composer'
import { CODING_SESSION_EVENT, notifyCodingSessionChanged, type CodingSessionEventDetail } from './coding-session-events'
import { CODING_PHASE_LABELS, codingModelLabel, codingSessionCapabilities } from './coding-session-model'
import { CodingPlanPill } from './coding-session-plan'
import { CodingCommitBadge, CodingPullRequestCard, CodingUncommittedChanges } from './coding-session-pr-card'
import { CodingActionRow, CodingActivityGroup, CodingAuthLine, CodingErrorCard, CodingLatestButton, CodingReasoning, CodingShimmer, CodingStaleCard, CodingTimeMarker } from './coding-session-stream'
import { buildStreamItems, buildStreamTurns, commitsByTurn, isCodingSessionGenerating, pendingElicitation, stopRequested, type StreamItem } from './coding-session-stream-model'
import './coding-session-stream.css'

/** Loads every activity of the session's AgentTask anchor, then only newer ones on each push. */
function useAnchorActivities(workspace: string, agentSessionId: string, issueId: string | undefined, revision: string) {
  const [activities, setActivities] = useState<ApplicationActivity[]>([])
  const [error, setError] = useState('')
  const [loaded, setLoaded] = useState(false)
  const [tick, setTick] = useState(0)
  const cursor = useRef({ key: '', after: '' })
  useEffect(() => {
    const bump = () => setTick(value => value + 1)
    const onTask = (event: Event) => {
      const id = (event as CustomEvent<unknown>).detail
      if (!id || id === issueId || id === agentSessionId) bump()
    }
    const onCoding = (event: Event) => {
      const detail = (event as CustomEvent<CodingSessionEventDetail>).detail
      if (!detail?.agentSessionId || detail.agentSessionId === agentSessionId) bump()
    }
    window.addEventListener(AGENT_TASK_ACTIVITY_EVENT, onTask)
    window.addEventListener(CODING_SESSION_EVENT, onCoding)
    return () => {
      window.removeEventListener(AGENT_TASK_ACTIVITY_EVENT, onTask)
      window.removeEventListener(CODING_SESSION_EVENT, onCoding)
    }
  }, [agentSessionId, issueId])
  useEffect(() => {
    const abort = new AbortController()
    const key = `${workspace}:${agentSessionId}`
    const load = async () => {
      const reset = cursor.current.key !== key
      let after = reset ? '' : cursor.current.after
      const next: ApplicationActivity[] = []
      let result
      do {
        result = await getApplicationTask(workspace, agentSessionId, after, abort.signal)
        next.push(...result.activities)
        if (result.activities.length) after = result.activities.at(-1)!.id
      } while (result.activities.length === 100 && !abort.signal.aborted)
      if (abort.signal.aborted) return
      cursor.current = { key, after }
      // An ephemeral step is replaced by the agent's next one: later activities win by id, and a newer ephemeral
      // activity drops the older ones.
      setActivities(previous => {
        const merged = [...new Map([...(reset ? [] : previous), ...next].map(activity => [activity.id, activity])).values()]
        const lastEphemeral = [...merged].reverse().find(activity => activity.ephemeral)
        return merged.filter(activity => !activity.ephemeral || activity === lastEphemeral)
      })
      setError('')
      setLoaded(true)
    }
    void load().catch(cause => { if (!abort.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause)) })
    return () => abort.abort()
  }, [workspace, agentSessionId, revision, tick])
  return { activities, error, loaded }
}

/** Follows the end of the stream while the reader is there; `away` once the end is 70px out of view (the "Latest" button). */
function useStreamFollow(scroller: RefObject<HTMLElement | null>, sentinel: RefObject<HTMLElement | null>, revision: string) {
  const [away, setAway] = useState(false)
  const pinned = useRef(true)
  useEffect(() => {
    const root = scroller.current, end = sentinel.current
    if (!root || !end || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver(([entry]) => { pinned.current = entry.isIntersecting; setAway(!entry.isIntersecting) }, { root, rootMargin: '0px 0px 70px 0px' })
    observer.observe(end)
    return () => observer.disconnect()
  }, [scroller, sentinel])
  useLayoutEffect(() => {
    // Keep following the stream while the reader is at the end.
    if (pinned.current) sentinel.current?.scrollIntoView?.({ block: 'end' })
  }, [revision, sentinel])
  return { away, jump: () => sentinel.current?.scrollIntoView?.({ behavior: 'smooth', block: 'end' }) }
}

const AGENT_TYPES = new Set(['thought', 'action', 'response', 'elicitation', 'error'])

type ConversationProps = {
  session: CodingSession
  data: BootstrapData
  onChange: (session: CodingSession) => void
  /** Opens the session's changes (the "Uncommitted changes" pill). */
  onOpenChanges?: () => void
  /** Composer extensions from the host (attachments). */
  composer?: Pick<CodingComposerProps, 'attachments' | 'toolbarStart' | 'uploading' | 'textareaProps' | 'hasPayload'> & { send?: (text: string) => Promise<CodingSession> }
}

/**
 * The agent panel's conversation: the activity stream in turns (time markers, prompts, folded work groups, responses,
 * errors, the PR card and commit badges), the "Latest" button, the plan pill and the composer.
 */
export function CodingSessionConversation({ session, data, onChange, onOpenChanges, composer }: ConversationProps) {
  const { t } = useI18n()
  const { activities, error, loaded } = useAnchorActivities(data.workspace.urlKey, session.agentSessionId, session.issueId, `${session.version}:${session.updatedAt}`)
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const scroller = useRef<HTMLDivElement>(null)
  const sentinel = useRef<HTMLDivElement>(null)
  const generating = isCodingSessionGenerating(session)
  const can = codingSessionCapabilities(session)
  const items = useMemo(() => buildStreamItems(activities, generating), [activities, generating])
  const turns = useMemo(() => buildStreamTurns(items), [items])
  const commits = useMemo(() => commitsByTurn(turns, session), [turns, session])
  const { away, jump } = useStreamFollow(scroller, sentinel, `${activities.length}:${activities.at(-1)?.id}:${generating}`)

  const run = async (action: () => Promise<CodingSession | void>, done?: string): Promise<boolean> => {
    if (busyRef.current) return false
    busyRef.current = true; setBusy(true)
    try {
      const next = await action()
      if (next && next.id === session.id) onChange(next)
      notifyCodingSessionChanged({ sessionId: session.id, agentSessionId: session.agentSessionId, issueId: session.issueId })
      if (done) toast.success(t(done))
      return true
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : t('Could not send message'))
      return false
    } finally { busyRef.current = false; setBusy(false) }
  }
  const retry = () => void run(() => restartCodingSession(session.id), 'Restarted coding session')
  const { send: hostSend, ...composerProps } = composer ?? {}
  const send = hostSend ?? ((text: string) => promptCodingSession(session.id, text))

  const elicitation = session.phase === 'awaitingUser' ? pendingElicitation(activities) : undefined
  const request: ComposerRequest | undefined = session.phase === 'awaitingApproval' ? { kind: 'approval' } : elicitation ? { kind: 'elicitation', activity: elicitation } : undefined
  const pr = session.latestPullRequest ?? session.pullRequests?.at(-1)
  const lastErrorId = items.reduce<string | undefined>((id, item) => item.kind === 'error' ? item.activity.id : id, undefined)
  const lastResponseIndex = items.map(item => item.kind === 'text' && item.activity.type === 'response').lastIndexOf(true)
  const lastResponse = lastResponseIndex >= 0 ? items[lastResponseIndex] : undefined
  const agentActive = activities.some(activity => AGENT_TYPES.has(activity.type))
  const allCommits = Object.values(session.commitsByTurnActivityId ?? {}).flat()
  const showUncommitted = Boolean(onOpenChanges && session.diffs?.length && !generating && !pr && !allCommits.length)
  const stale = session.state === 'stale' && !session.stoppedBy && loaded && !agentActive
  const missingError = session.state === 'error' && loaded && !lastErrorId
  const lastAgentText = [...items].reverse().find(item => item.kind === 'text') as Extract<StreamItem, { kind: 'text' }> | undefined
  const creator = data.users.find(user => user.id === session.creatorId)
  const issue = session.issueId ? data.issues.find(candidate => candidate.id === session.issueId) : undefined
  const model = codingModelLabel(session)

  const userName = (id: string) => {
    const user = data.users.find(candidate => candidate.id === id)
    return id === data.viewer.id ? t('You') : user?.displayName || user?.name || t('User')
  }
  const renderItem = (item: StreamItem, key: string): ReactNode => {
    switch (item.kind) {
      case 'group': return <CodingActivityGroup key={key} group={item}/>
      case 'action': return <CodingActionRow key={key} action={item}/>
      case 'reasoning': return <CodingReasoning key={key} activity={item.activity}/>
      case 'thinking': return <div key={key} className="cs-action" data-loading role="status"><div className="cs-action-header"><span className="cs-action-label"><CodingShimmer>{session.state === 'pending' && CODING_PHASE_LABELS[session.phase] ? `${t(CODING_PHASE_LABELS[session.phase])}…` : t('Thinking…')}</CodingShimmer></span></div></div>
      case 'text': return <Fragment key={key}>
        <AgentAnswerText className="coding-session-markdown cs-text" data={data} markdown={item.activity.body}/>
        {item === lastResponse && pr && <CodingPullRequestCard session={session} pr={pr} reviews={data.reviews ?? []} workspace={data.workspace.urlKey}/>}
      </Fragment>
      case 'auth': return <CodingAuthLine key={key} activity={item.activity} data={data}/>
      case 'error': return <CodingErrorCard key={key} activity={item.activity} session={session} data={data} busy={busy} onRetry={retry} retry={session.state === 'error' && can.restart && item.activity.id === lastErrorId}/>
      case 'prompt': return null
    }
  }
  const copy = async (text: string) => {
    try { await navigator.clipboard.writeText(text); toast.success(t('Copied message')) }
    catch { toast.error(t('Could not copy')) }
  }
  const links = (session.externalUrls ?? []).filter(link => /^https?:\/\//i.test(link.url))
  const startedByAgent = turns[0] && !turns[0].prompt
  return <>
    <div ref={scroller} className="coding-agent-stream cs-stream" role="group" aria-label={t('Session stream')}>
      {startedByAgent && <p className="cs-start-line">
        <UserAvatar className="coding-session-prompt-avatar" name={creator?.displayName || creator?.name || t('Flow')} avatarUrl={creator?.avatarUrl}/>
        <span><strong data-i18n-ignore>{creator ? creator.displayName || creator.name : t('Flow')}</strong> {issue ? <>{t('started the session from')} <AppLink href={issuePath(data.workspace.urlKey, issue)} data-i18n-ignore>{issue.identifier}</AppLink></> : t('started the session')}</span>
      </p>}
      {session.modelSelection === 'auto' && session.model && <p className="cs-start-line"><span><strong>{t('Flow')}</strong> {t('routed this session to')} <strong data-i18n-ignore>{model.model.replace(/ \(Auto\)$/, '')}</strong></span></p>}
      {turns.map((turn, index) => {
        const last = index === turns.length - 1
        const turnCommits = commits.get(turn.id)
        return <section key={turn.id} className="cs-turn" data-last={last || undefined}>
          {turn.showTime && turn.at && <CodingTimeMarker at={turn.at}/>}
          {turn.prompt && <div className="cs-prompt-row">
            <div className="coding-session-prompt">
              <header><UserAvatar className="coding-session-prompt-avatar" name={userName(turn.prompt.activity.actorId)} avatarUrl={data.users.find(user => user.id === turn.prompt!.activity.actorId)?.avatarUrl}/><strong data-i18n-ignore>{userName(turn.prompt.activity.actorId)}</strong></header>
              {turn.prompt.stop ? <p><em>{t('Stop requested')}</em></p> : <p data-i18n-ignore>{turn.prompt.activity.body}</p>}
            </div>
          </div>}
          {turn.items.length > 0 && <div className="cs-turn-items">
            {turn.items.map((item, position) => renderItem(item, item.kind === 'thinking' ? 'thinking' : item.kind === 'group' ? item.id : `${item.activity.id}-${position}`))}
            {turnCommits?.length ? <CodingCommitBadge commits={turnCommits} pr={pr ?? (session.repository ? { codeHost: session.codeHost ?? 'github', hostName: session.hostName ?? '', repository: session.repository } : undefined)}/> : null}
          </div>}
        </section>
      })}
      {session.phase === 'awaitingApproval' && <p className="cs-text cs-approval-text">{t('This session comes from an external source. A member must approve it before the agent starts.')}</p>}
      {pr && !lastResponse && !generating && <CodingPullRequestCard session={session} pr={pr} reviews={data.reviews ?? []} workspace={data.workspace.urlKey}/>}
      {showUncommitted && onOpenChanges && <CodingUncommittedChanges session={session} onOpen={onOpenChanges}/>}
      {missingError && <CodingErrorCard session={session} data={data} busy={busy} onRetry={retry} retry={can.restart}/>}
      {stale && <CodingStaleCard busy={busy} onRetry={retry}/>}
      {error && <p className="coding-session-error-text" role="alert">{error}</p>}
      {!generating && (lastAgentText || links.length > 0) && <footer className="cs-turn-footer" data-links={links.length > 0 || undefined}>
        {links.length === 1 && <a className="cs-button is-ghost is-small" href={links[0].url} target="_blank" rel="noreferrer"><span data-i18n-ignore>{links[0].label}</span><ArrowUpRight size={14} aria-hidden="true"/></a>}
        {links.length > 1 && <DropdownMenu>
          <DropdownMenuTrigger asChild><button type="button" className="cs-button is-ghost is-small">{t('Links')}<ChevronDown size={14} aria-hidden="true"/></button></DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="coding-session-menu">{links.map(link => <DropdownMenuItem key={link.url} onSelect={() => window.open(link.url, '_blank', 'noopener')}><span data-i18n-ignore>{link.label}</span></DropdownMenuItem>)}</DropdownMenuContent>
        </DropdownMenu>}
        <span className="cs-composer-spacer"/>
        {lastAgentText && <button type="button" className="coding-session-icon-button cs-copy" aria-label={t('Copy message')} onClick={() => void copy(lastAgentText.activity.body)}><AgentCopyGlyph size={14}/></button>}
      </footer>}
      <div ref={sentinel} className="cs-stream-end" aria-hidden="true"/>
    </div>
    {away && <CodingLatestButton working={generating} onClick={jump}/>}
    <div className="cs-composer-area">
      <CodingPlanPill plan={session.plan} updatedAt={session.updatedAt} complete={session.state === 'complete'}/>
      {(can.prompt || request) && <CodingComposer data={data} sessionId={session.id} generating={generating} stopPending={stopRequested(session)} canPrompt={can.prompt} busy={busy} request={request}
        onSend={text => run(() => send(text))}
        onStop={() => void run(() => stopCodingSession(session.id))}
        onApprove={async reject => { await run(() => approveCodingSession(session.id, reject), reject ? 'Rejected coding session' : 'Approved coding session') }}
        {...composerProps}/>}
    </div>
  </>
}
