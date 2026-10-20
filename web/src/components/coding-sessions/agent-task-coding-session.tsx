import { MoreHorizontal } from 'lucide-react'
import { activityTimeLabel } from '@/components/activity/issue-activity-model'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import { setCodingSessionSubscription } from '@/lib/coding-sessions-api'
import { toast } from '@/lib/toast'
import type { BootstrapData } from '@/types/flow'
import type { CodingSession } from '@/types/coding-sessions'
import { CodingSessionStatusRow } from './coding-session-card'
import { notifyCodingSessionChanged } from './coding-session-events'
import { codingSessionSubscribed } from './coding-session-page-model'
import { useCodingSessionOpener } from './use-coding-session-opener'
import { useCodingAvailability, useCodingSession, useCodingSessions, useDismissedCodingSessions } from './use-coding-sessions'
import './coding-sessions.css'

/** The agent task fields this needs. `codingSessionId` is set by the server when the task anchors a coding session. */
type AnchorTask = { id: string; issueId?: string; codingSessionId?: string }

/**
 * Mounted in each agent session thread (issue-agent-tasks.tsx): when the task anchors a coding session, the thread shows
 * the session's status row ("Thinking… 02:42", "Error …"), like the reference app's session card in issue activity;
 * the row opens the session page.
 */
export function AgentTaskCodingSession({ task, data }: { task: AnchorTask; data: BootstrapData }) {
  const available = useCodingAvailability(data)
  if (!available) return null
  if (task.codingSessionId) return <LiveEmbeddedSession sessionId={task.codingSessionId} data={data}/>
  return task.issueId ? <IssueAnchoredSession task={task} issueId={task.issueId} data={data}/> : null
}

/** Without `codingSessionId` on the task, the issue's sessions are listed (one shared request) and matched by anchor. */
function IssueAnchoredSession({ task, issueId, data }: { task: AnchorTask; issueId: string; data: BootstrapData }) {
  const { sessions } = useCodingSessions({ issueId })
  const match = sessions.find(session => session.agentSessionId === task.id)
  return match ? <LiveEmbeddedSession sessionId={match.id} initial={match} data={data}/> : null
}

/** "now", "6min", "2h": the card header's short age. */
function shortAge(at: string | undefined, locale: string) {
  if (!at || Number.isNaN(Date.parse(at))) return ''
  const label = activityTimeLabel(at, Date.now(), locale)
  if (locale === 'zh-CN') return label
  return label === 'just now' ? 'now' : label.replace(/ ago$/, '').replace(/m$/, 'min')
}

/**
 * A delegated coding session in issue activity, like the reference app's session card: "<Agent> started by <member>
 * <age>" with the chat options (Copy link, Unsubscribe from chat, Archive) over the session's status row. The
 * conversation itself lives in the session view, so the card shows no messages.
 */
export function CodingSessionActivityCard({ task, data, agentName }: { task: AnchorTask & { creatorId: string; createdAt?: string; updatedAt?: string }; data: BootstrapData; agentName: string }) {
  const { t, locale } = useI18n()
  const available = useCodingAvailability(data)
  const { session, apply } = useCodingSession(task.codingSessionId ?? '')
  const { dismissed, dismiss } = useDismissedCodingSessions(data.workspace.urlKey, data.viewer.id)
  const opener = useCodingSessionOpener(session, data)
  if (!available || !task.codingSessionId || dismissed.has(task.codingSessionId)) return null
  const creator = data.users.find(user => user.id === (session?.creatorId || task.creatorId))
  const agent = data.users.find(user => user.app)
  const at = session?.createdAt || task.createdAt || task.updatedAt
  const age = shortAge(at, locale)
  const subscribed = session ? codingSessionSubscribed(session, data.viewer.id) : false
  const copyLink = async () => {
    const link = `${location.href.split('#')[0]}#agent-session-${task.id}`
    try { await navigator.clipboard.writeText(link); toast.success(t('Link copied')) }
    catch { toast.error(t('Could not copy')) }
  }
  const toggleSubscription = async () => {
    if (!session) return
    try {
      apply(await setCodingSessionSubscription(session.id, !subscribed))
      notifyCodingSessionChanged({ sessionId: session.id, agentSessionId: session.agentSessionId, issueId: session.issueId })
    } catch (cause) { toast.error(cause instanceof Error ? cause.message : t('Could not update subscription')) }
  }
  return <article className="coding-session-activity-card" data-state={session?.state} aria-label={t('Coding session')}>
    <header className="coding-session-activity-header">
      <span className="coding-session-activity-avatar" aria-label={agentName}><UserAvatar className="coding-session-activity-avatar-image" name={agentName} avatarUrl={agent?.avatarUrl}/></span>
      <span className="coding-session-activity-byline">
        <strong data-i18n-ignore>{agentName}</strong>
        {creator && <><span>{t('started by')}</span><strong data-i18n-ignore>{creator.displayName || creator.name}</strong></>}
        {age && <time dateTime={at} title={new Date(at!).toLocaleString(locale)}>{age}</time>}
      </span>
      <DropdownMenu>
        <DropdownMenuTrigger asChild><button type="button" className="coding-session-icon-button coding-session-activity-menu" aria-label={t('Agent chat options')}><MoreHorizontal size={14}/></button></DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="coding-session-menu">
          <DropdownMenuItem onSelect={() => void copyLink()}>{t('Copy link')}</DropdownMenuItem>
          {session && <DropdownMenuItem onSelect={() => void toggleSubscription()}>{t(subscribed ? 'Unsubscribe from chat' : 'Subscribe to chat')}</DropdownMenuItem>}
          <DropdownMenuSeparator/>
          <DropdownMenuItem onSelect={() => dismiss(task.codingSessionId!)}>{t('Archive')}</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </header>
    {session && <CodingSessionStatusRow session={session} data={data} {...opener.rowProps}/>}
    {opener.dialog}
  </article>
}

function LiveEmbeddedSession({ sessionId, initial, data }: { sessionId: string; initial?: CodingSession; data: BootstrapData }) {
  const { session } = useCodingSession(sessionId, initial)
  const opener = useCodingSessionOpener(session, data)
  if (!session) return null
  return <div className="coding-session-embedded" data-state={session.state}>
    <CodingSessionStatusRow session={session} data={data} {...opener.rowProps}/>
    {opener.dialog}
  </div>
}
