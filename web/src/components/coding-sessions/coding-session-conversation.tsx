import { Fragment, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react'
import { ArrowUpRight, ChevronDown } from 'lucide-react'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { approveCodingSession, promptCodingSession, restartCodingSession, stopCodingSession } from '@/lib/coding-sessions-api'
import { AGENT_TASK_ACTIVITY_EVENT, getApplicationTask, type ApplicationActivity } from '@/lib/application-agents'
import { AgentAnswerText } from '@/components/agent/agent-answer'
import { AgentMessageAttachments } from '@/components/agent/agent-message-attachments'
import { activityTimeLabel } from '@/components/activity/issue-activity-model'
import { AgentCopyGlyph } from '@/components/ui/agent-glyph'
import { AppLink } from '@/components/ui/app-link'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import { issuePath } from '@/lib/app-routes'
import { toast } from '@/lib/toast'
import type { BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { CodingComposer, type ComposerRequest } from './coding-session-composer'
import { CODING_SESSION_EVENT, notifyCodingSessionChanged, type CodingSessionEventDetail } from './coding-session-events'
import { CODING_PHASE_LABELS, CODING_STOPPED_MESSAGE, codingModelLabel, codingSessionCapabilities } from './coding-session-model'
import { CodingPlanPill } from './coding-session-plan'
import { CodingCommitBadge, CodingPullRequestCard, CodingUncommittedChanges, type CodingCommitStats } from './coding-session-pr-card'
import { CodingActionRow, CodingActivityGroup, CodingAuthLine, CodingErrorCard, CodingLatestButton, CodingReasoning, CodingShimmer, CodingStaleCard, CodingTimeMarker } from './coding-session-stream'
import { buildStreamItems, buildStreamTurns, commitsByTurn, isCodingSessionGenerating, pendingElicitation, stopRequested, type StreamItem } from './coding-session-stream-model'
import { useCodingSessionArtifacts } from './artifacts/use-coding-session-artifacts'
import { artifactSrc, screenshotsByTurn } from './artifacts/coding-artifacts-model'
import { openLightbox } from '@/components/editor/lightbox-bridge'
import type { CodingArtifact } from '@/types/coding-sessions'
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
  return { activities, error, loaded, retry: () => setTick(value => value + 1) }
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

/** A prompt's time: "just now", "3min ago", with the full date as the tooltip. */
function PromptTime({ at }: { at: string }) {
  const { locale } = useI18n()
  const date = new Date(at)
  if (!at || Number.isNaN(date.getTime())) return null
  const full = date.toLocaleString(locale, { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit' })
  return <time dateTime={at} title={full} aria-label={full}>{activityTimeLabel(at, Date.now(), locale).replace(/m(?= ago$)/, 'min')}</time>
}

/** A prompt's text with its backticked spans as inline code chips (the rest stays as typed, line breaks included). */
function PromptText({ text }: { text: string }) {
  const parts = text.split(/(`[^`\n]+`)/g)
  return <>{parts.map((part, index) => part.length > 2 && part.startsWith('`') && part.endsWith('`') ? <code key={index}>{part.slice(1, -1)}</code> : part)}</>
}

const AGENT_TYPES = new Set(['thought', 'action', 'response', 'elicitation', 'error'])

type ConversationProps = {
  session: CodingSession
  data: BootstrapData
  onChange: (session: CodingSession) => void
  /** Opens the session's changes (the "Uncommitted changes" pill). */
  onOpenChanges?: () => void
}

/**
 * The agent panel's conversation: the activity stream in turns (time markers, prompts, folded work groups, responses,
 * errors, the PR card and commit badges), the "Latest" button, the plan pill and the composer.
 */
export function CodingSessionConversation({ session, data, onChange, onOpenChanges }: ConversationProps) {
  const { t } = useI18n()
  const { activities, error, loaded, retry: reloadMessages } = useAnchorActivities(data.workspace.urlKey, session.agentSessionId, session.issueId, `${session.version}:${session.updatedAt}`)
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const scroller = useRef<HTMLDivElement>(null)
  const sentinel = useRef<HTMLDivElement>(null)
  const generating = isCodingSessionGenerating(session)
  const can = codingSessionCapabilities(session)
  const items = useMemo(() => buildStreamItems(activities, generating), [activities, generating])
  const turns = useMemo(() => buildStreamTurns(items), [items])
  const commits = useMemo(() => commitsByTurn(turns, session), [turns, session])
  // The agent's screenshots sit under the turn that took them, at the end of its message (as in the reference app).
  const { items: artifacts, expired: artifactExpired } = useCodingSessionArtifacts(session.id, session.artifacts, session.updatedAt)
  const shots = useMemo(() => screenshotsByTurn(turns.map(turn => ({ id: turn.id, promptActivityId: turn.prompt?.activity.id })), session.id, artifacts ?? []), [turns, session.id, artifacts])
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

  const elicitation = session.phase === 'awaitingUser' ? pendingElicitation(activities) : undefined
  const request: ComposerRequest | undefined = session.phase === 'awaitingApproval' ? { kind: 'approval' } : elicitation ? { kind: 'elicitation', activity: elicitation } : undefined
  const pr = session.latestPullRequest ?? session.pullRequests?.at(-1)
  const lastErrorId = items.reduce<string | undefined>((id, item) => item.kind === 'error' ? item.activity.id : id, undefined)
  const lastResponseIndex = items.map(item => item.kind === 'text' && item.activity.type === 'response').lastIndexOf(true)
  const lastResponse = lastResponseIndex >= 0 ? items[lastResponseIndex] : undefined
  const agentActive = activities.some(activity => AGENT_TYPES.has(activity.type))
  const allCommits = Object.values(session.commitsByTurnActivityId ?? {}).flat()
  // The commit chip shows the session's file and line totals while one turn holds every commit (they are that turn's
  // changes); with commits in several turns the per-turn totals are unknown and the chip shows the SHA only.
  const diffs = session.diffs ?? []
  const commitStats: CodingCommitStats | undefined = diffs.length && commits.size === 1
    ? { files: diffs.length, additions: diffs.reduce((sum, diff) => sum + diff.additions, 0), deletions: diffs.reduce((sum, diff) => sum + diff.deletions, 0) }
    : undefined
  const showUncommitted = Boolean(onOpenChanges && session.diffs?.length && !generating && !pr && !allCommits.length)
  const stale = session.state === 'stale' && !session.stoppedBy && loaded && !agentActive
  // A member's stop ends with the agent's "Stopped. Send another prompt…" when the thread has no reply after it.
  const stopReply = session.state === 'stale' && Boolean(session.stoppedBy) && loaded && items.at(-1)?.kind !== 'text'
  // The session's own error (errorCode / error from GET session) when the thread has no error activity, or when the
  // thread could not be loaded at all: the reason must show either way.
  const missingError = session.state === 'error' && !lastErrorId && (loaded || Boolean(error))
  const lastAgentText = [...items].reverse().find(item => item.kind === 'text') as Extract<StreamItem, { kind: 'text' }> | undefined
  const creator = data.users.find(user => user.id === session.creatorId)
  const issue = session.issueId ? data.issues.find(candidate => candidate.id === session.issueId) : undefined
  const model = codingModelLabel(session)

  // Prompts carry the author's name (the viewer's own too) and a relative time with the full date on hover.
  const userName = (id: string) => {
    const user = data.users.find(candidate => candidate.id === id) ?? (id === data.viewer.id ? data.viewer : undefined)
    return user?.displayName || user?.name || t('User')
  }
  const renderItem = (item: StreamItem, key: string): ReactNode => {
    switch (item.kind) {
      case 'group': return <CodingActivityGroup key={key} group={item}/>
      case 'action': return <CodingActionRow key={key} action={item}/>
      case 'reasoning': return <CodingReasoning key={key} activity={item.activity}/>
      case 'thinking': return <div key={key} className="cs-action" data-loading role="status"><div className="cs-action-header"><span className="cs-action-label"><CodingShimmer>{session.state === 'pending' && CODING_PHASE_LABELS[session.phase] ? `${t(CODING_PHASE_LABELS[session.phase])}…` : t('Thinking…')}</CodingShimmer></span></div></div>
      case 'text': return <Fragment key={key}>
        <AgentAnswerText className="coding-session-markdown cs-text" data={data} markdown={item.activity.body}/>
        {item === lastResponse && pr && !allCommits.length && <CodingPullRequestCard session={session} pr={pr} reviews={data.reviews ?? []} workspace={data.workspace.urlKey}/>}
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
  // A delegated session starts from its issue: the first turn shows the delegating member's "Work on <issue>" prompt.
  const issuePrompt = startedByAgent && issue
  // "Flow routed this session to <model>" follows the first prompt (above the work it routed).
  const routed = session.modelSelection === 'auto' && session.model
    ? <p className="cs-start-line"><span><strong>{t('Flow')}</strong> {t('routed this session to')} <strong data-i18n-ignore>{model.model.replace(/ \(Auto\)$/, '')}</strong></span></p>
    : null
  return <>
    <div ref={scroller} className="coding-agent-stream cs-stream" role="group" aria-label={t('Session stream')}>
      {startedByAgent && !issuePrompt && <p className="cs-start-line">
        <UserAvatar className="coding-session-prompt-avatar" name={creator?.displayName || creator?.name || t('Flow')} avatarUrl={creator?.avatarUrl}/>
        <span><strong data-i18n-ignore>{creator ? creator.displayName || creator.name : t('Flow')}</strong> {issue ? <>{t('started the session from')} <AppLink href={issuePath(data.workspace.urlKey, issue)} data-i18n-ignore>{issue.identifier}</AppLink></> : t('started the session')}</span>
      </p>}
      {routed && !turns[0]?.prompt && !issuePrompt && routed}
      {turns.map((turn, index) => {
        const last = index === turns.length - 1
        const turnCommits = commits.get(turn.id)
        return <section key={turn.id} className="cs-turn" data-last={last || undefined}>
          {turn.showTime && turn.at && <CodingTimeMarker at={turn.at}/>}
          {turn.prompt && <div className="cs-prompt-row">
            <div className="coding-session-prompt">
              <header><UserAvatar className="coding-session-prompt-avatar" name={userName(turn.prompt.activity.actorId)} avatarUrl={data.users.find(user => user.id === turn.prompt!.activity.actorId)?.avatarUrl}/><strong data-i18n-ignore>{userName(turn.prompt.activity.actorId)}</strong><PromptTime at={turn.prompt.activity.createdAt}/></header>
              {turn.prompt.stop ? <p><em>{t('Stop requested')}</em></p> : turn.prompt.activity.body && <p data-i18n-ignore><PromptText text={turn.prompt.activity.body}/></p>}
              <AgentMessageAttachments attachments={turn.prompt.activity.metadata?.attachments}/>
            </div>
          </div>}
          {index === 0 && !turn.prompt && issuePrompt && issue && <div className="cs-prompt-row">
            <div className="coding-session-prompt">
              <header><UserAvatar className="coding-session-prompt-avatar" name={userName(session.creatorId)} avatarUrl={creator?.avatarUrl}/><strong data-i18n-ignore>{userName(session.creatorId)}</strong><PromptTime at={session.createdAt}/></header>
              <p>{t('Work on {issue}').split('{issue}').map((part, position) => position ? <Fragment key={position}><AppLink href={issuePath(data.workspace.urlKey, issue)} data-i18n-ignore>{issue.identifier}</AppLink>{part}</Fragment> : part)}</p>
            </div>
          </div>}
          {index === 0 && (turn.prompt || issuePrompt) && routed}
          {(turn.items.length > 0 || (last && stopReply)) && <div className="cs-turn-items">
            {turn.items.map((item, position) => renderItem(item, item.kind === 'thinking' ? 'thinking' : item.kind === 'group' ? item.id : `${item.activity.id}-${position}`))}
            {shots.get(turn.id)?.length ? <CodingTurnScreenshots shots={shots.get(turn.id)!} all={[...shots.values()].flat()} onExpired={artifactExpired}/> : null}
            {last && stopReply && <p className="cs-text cs-stop-reply">{t(CODING_STOPPED_MESSAGE)}</p>}
            {turnCommits?.length ? <CodingCommitBadge commits={turnCommits} stats={commitStats} onOpenChanges={onOpenChanges} pr={pr ?? (session.repository ? { codeHost: session.codeHost ?? 'github', hostName: session.hostName ?? '', repository: session.repository } : undefined)}/> : null}
          </div>}
        </section>
      })}
      {session.phase === 'awaitingApproval' && <p className="cs-text cs-approval-text">{t('This session comes from an external source. A member must approve it before the agent starts.')}</p>}
      {pr && !lastResponse && !generating && !allCommits.length && <CodingPullRequestCard session={session} pr={pr} reviews={data.reviews ?? []} workspace={data.workspace.urlKey}/>}
      {showUncommitted && onOpenChanges && <CodingUncommittedChanges session={session} onOpen={onOpenChanges}/>}
      {missingError && <CodingErrorCard session={session} data={data} busy={busy} onRetry={retry} retry={can.restart}/>}
      {stale && <CodingStaleCard busy={busy} onRetry={retry}/>}
      {error && <p className="coding-session-error-text cs-messages-error" role="alert" title={error}>
        <span>{t('Couldn’t load this session’s messages.')}</span>
        <button type="button" className="cs-button is-ghost is-small" onClick={reloadMessages}>{t('Retry')}</button>
      </p>}
      {!generating && (lastAgentText || links.length > 0) && <footer className="cs-turn-footer" data-links={links.length > 0 || undefined}>
        {links.length === 1 && <a className="cs-button is-ghost is-small" href={links[0].url} target="_blank" rel="noreferrer"><span data-i18n-ignore>{links[0].label}</span><ArrowUpRight size={14} aria-hidden="true"/></a>}
        {links.length > 1 && <DropdownMenu>
          <DropdownMenuTrigger asChild><button type="button" className="cs-button is-ghost is-small">{t('Links')}<ChevronDown size={14} aria-hidden="true"/></button></DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="coding-session-menu">{links.map(link => <DropdownMenuItem key={link.url} onSelect={() => window.open(link.url, '_blank', 'noopener')}><span data-i18n-ignore>{link.label}</span></DropdownMenuItem>)}</DropdownMenuContent>
        </DropdownMenu>}
        <span className="cs-composer-spacer"/>
        {lastAgentText && <button type="button" className="coding-session-icon-button cs-copy" aria-label={t('Copy message')} onClick={() => void copy(lastAgentText.activity.body)}><AgentCopyGlyph size={14}/></button>}
      </footer>}
      {session.archivedAt && <p className="cs-archived-divider" role="status"><span><FlowGlyph name="agentSessionDismissed" size={16}/>{t('Session archived')}</span></p>}
      <div ref={sentinel} className="cs-stream-end" aria-hidden="true"/>
    </div>
    {away && <CodingLatestButton working={generating} onClick={jump}/>}
    <div className="cs-composer-area">
      <CodingPlanPill plan={session.plan} updatedAt={session.updatedAt} complete={session.state === 'complete'}/>
      {(can.prompt || request) && !session.archivedAt && <CodingComposer data={data} sessionId={session.id} generating={generating} stopPending={stopRequested(session)} canPrompt={can.prompt} busy={busy} request={request}
        onSend={(text, files) => run(() => promptCodingSession(session.id, text, undefined, files))}
        onStop={() => void run(() => stopCodingSession(session.id))}
        onApprove={async reject => { await run(() => approveCodingSession(session.id, reject), reject ? 'Rejected coding session' : 'Approved coding session') }}/>}
    </div>
  </>
}

/**
 * A turn's screenshots in the conversation: each image at the message width (up to 600px tall, 6px corners), opening
 * the image viewer over every inline screenshot of the session.
 */
function CodingTurnScreenshots({ shots, all, onExpired }: { shots: CodingArtifact[]; all: CodingArtifact[]; onExpired: (artifact: CodingArtifact) => void }) {
  const { t } = useI18n()
  const open = (shot: CodingArtifact) => {
    const index = Math.max(0, all.findIndex(item => item.id === shot.id))
    if (!openLightbox(all.map(item => ({ src: artifactSrc(item), alt: item.label, title: item.label })), index)) window.open(artifactSrc(shot), '_blank', 'noopener')
  }
  return <div className="cs-shots">
    {shots.map(shot => <button key={shot.id} type="button" className="cs-shot" aria-label={t('View image')} onClick={() => open(shot)}>
      <img src={artifactSrc(shot)} alt={shot.label} loading="lazy" data-i18n-ignore onError={() => onExpired(shot)}/>
    </button>)}
  </div>
}
