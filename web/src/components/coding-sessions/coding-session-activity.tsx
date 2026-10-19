import { useEffect, useMemo, useRef, useState } from 'react'
import { AlertTriangle, ChevronRight, ExternalLink } from 'lucide-react'
import { activityTimeLabel } from '@/components/activity/issue-activity-model'
import { AgentCopyGlyph } from '@/components/ui/agent-glyph'
import { UserAvatar } from '@/components/ui/user-avatar'
import { toast } from '@/lib/toast'
import { AGENT_TASK_ACTIVITY_EVENT, getApplicationTask, recommendedChoiceValue, replyApplicationTask, type ApplicationActivity, type ApplicationTask } from '@/lib/application-agents'
import { AgentAnswerText, AgentChoicePills } from '@/components/agent/agent-answer'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData } from '@/types/flow'
import { CODING_SESSION_EVENT, type CodingSessionEventDetail } from './coding-session-events'

/** `all`: the full stream (session view, chat card, dialog). `steps`: only thoughts, actions and errors, folded (inside an issue thread, which already shows the replies). */
export type CodingActivityMode = 'all' | 'steps'

const STEP_TYPES = new Set(['thought', 'action', 'error'])
const SHOWN_TYPES = new Set(['prompt', 'thought', 'action', 'response', 'elicitation', 'error'])

/** Loads every activity of the session's AgentTask anchor, then only newer ones on each push. */
function useAnchorActivities(workspace: string, agentSessionId: string, issueId: string | undefined, revision: string) {
  const [activities, setActivities] = useState<ApplicationActivity[]>([])
  const [task, setTask] = useState<ApplicationTask>()
  const [error, setError] = useState('')
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
      setTask(result.session)
      setActivities(previous => [...new Map([...(reset ? [] : previous), ...next].map(activity => [activity.id, activity])).values()])
      setError('')
    }
    void load().catch(cause => { if (!abort.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause)) })
    return () => abort.abort()
  }, [workspace, agentSessionId, revision, tick])
  return { activities, task, setTask, error }
}

/**
 * The coding session's activity stream (§16 mapping): the agent's thoughts, actions (commands, edits, searches),
 * responses, questions (elicitations, with select options) and errors, read from the AgentTask anchor.
 */
export function CodingSessionActivity({ data, agentSessionId, issueId, mode, awaitingInput, revision }: { data: BootstrapData; agentSessionId: string; issueId?: string; mode: CodingActivityMode; /** The session waits on the user (phase awaitingUser): the latest open question takes an answer. */ awaitingInput: boolean; /** Changes whenever the session changes (refetches newer activities). */ revision: string }) {
  const { t, locale } = useI18n()
  const relativeTime = (value: string) => Number.isNaN(Date.parse(value)) ? '' : activityTimeLabel(value, Date.now(), locale).replace(/m(?= ago$)/, 'min')
  const { activities, task, setTask, error } = useAnchorActivities(data.workspace.urlKey, agentSessionId, issueId, revision)
  const [expanded, setExpanded] = useState(mode === 'all')
  const [busy, setBusy] = useState(false)
  const [answerError, setAnswerError] = useState('')
  const items = useMemo(() => {
    const deleted = new Set(activities.filter(activity => activity.type === 'deleted').map(activity => activity.metadata?.activityId))
    return activities.filter(activity => SHOWN_TYPES.has(activity.type) && !deleted.has(activity.id) && (mode === 'all' || STEP_TYPES.has(activity.type)))
  }, [activities, mode])
  // An elicitation is answered by the next human prompt after it.
  const answers = useMemo(() => {
    const map = new Map<string, string>()
    let open: string | undefined
    for (const activity of activities) {
      if (activity.type === 'elicitation') open = activity.id
      else if (activity.type === 'prompt' && open) { map.set(open, activity.body); open = undefined }
    }
    return map
  }, [activities])
  const pending = awaitingInput ? [...items].reverse().find(activity => activity.type === 'elicitation' && !answers.has(activity.id)) : undefined
  const answer = async (value: string) => {
    if (!task || busy) return
    setBusy(true); setAnswerError('')
    try { setTask(await replyApplicationTask(data.workspace.urlKey, task, 'prompt', value)) }
    catch (cause) { setAnswerError(cause instanceof Error ? cause.message : String(cause)) }
    finally { setBusy(false) }
  }
  if (!items.length) return error ? <p className="coding-session-error-text" role="alert">{error}</p> : null
  const userName = (id: string) => {
    const user = data.users.find(candidate => candidate.id === id)
    return id === data.viewer.id ? t('You') : user?.displayName || user?.name || t('User')
  }
  const lastAgent = [...items].reverse().find(activity => activity.type === 'response' || activity.type === 'error' || activity.type === 'elicitation')
  const copy = async (text: string) => {
    try { await navigator.clipboard.writeText(text); toast.success(t('Copied message')) }
    catch { toast.error(t('Could not copy')) }
  }
  const list = <ol className="coding-session-activity" aria-label={t('Session activity')}>
    {items.map(activity => {
      const options = (activity.signal === 'select' ? activity.signalMetadata?.options ?? [] : []).map(option => ({ label: option.label || option.value, value: option.value }))
      const answered = answers.get(activity.id)
      const user = activity.type === 'prompt' ? data.users.find(candidate => candidate.id === activity.actorId) : undefined
      return <li key={activity.id} className="coding-session-activity-item" data-type={activity.type}>
        {activity.type === 'prompt' && <div className="coding-session-prompt">
          <header><UserAvatar className="coding-session-prompt-avatar" name={user?.displayName || user?.name || userName(activity.actorId)} avatarUrl={user?.avatarUrl}/><strong data-i18n-ignore>{userName(activity.actorId)}</strong>{activity.createdAt && <time dateTime={activity.createdAt}>{relativeTime(activity.createdAt)}</time>}</header>
          <p data-i18n-ignore>{activity.body}</p>
        </div>}
        {activity.type === 'thought' && <p className="coding-session-thought" data-i18n-ignore>{activity.body}</p>}
        {activity.type === 'action' && <ActionRow activity={activity}/>}
        {activity.type === 'response' && <AgentAnswerText className="coding-session-markdown" data={data} markdown={activity.body}/>}
        {activity.type === 'error' && <div className="coding-session-callout" data-kind="error">
          <div className="coding-session-callout-title"><AlertTriangle size={14} aria-hidden="true"/><strong>{t('Error')}</strong></div>
          <p className="coding-session-activity-error" role="alert" data-i18n-ignore>{activity.body}</p>
        </div>}
        {activity.type === 'elicitation' && <>
          <AgentAnswerText className="coding-session-markdown" data={data} markdown={activity.body}/>
          {options.length > 0 && <AgentChoicePills choices={options} disabled={busy || pending?.id !== activity.id} onSelect={choice => void answer(choice.value)} recommended={activity.signal === 'select' ? recommendedChoiceValue(activity.signalMetadata) : undefined} selected={answered}/>}
          {activity.signal === 'auth' && !answered && activity.signalMetadata?.url && /^https?:\/\//i.test(activity.signalMetadata.url) && <a className="coding-session-link" href={activity.signalMetadata.url} target="_blank" rel="noreferrer"><ExternalLink size={13} aria-hidden="true"/>{t('Link account')}</a>}
        </>}
        {mode === 'all' && activity === lastAgent && activity.body && <button type="button" className="coding-session-icon-button coding-session-copy" aria-label={t('Copy message')} onClick={() => void copy(activity.body)}><AgentCopyGlyph size={14}/></button>}
      </li>
    })}
  </ol>
  const steps = items.length
  return <div className="coding-session-activity-block">
    {mode === 'steps'
      ? <button type="button" className="coding-session-disclosure" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>
        {t(expanded ? 'Hide steps' : 'Show {count} steps').replace('{count}', String(steps))}<ChevronRight size={16} aria-hidden="true" data-open={expanded || undefined}/>
      </button>
      : null}
    {expanded && list}
    {answerError && <p className="coding-session-error-text" role="alert">{answerError}</p>}
  </div>
}

function ActionRow({ activity }: { activity: ApplicationActivity }) {
  const { t } = useI18n()
  const action = activity.metadata?.action || activity.body
  const parameter = activity.metadata?.parameter
  const result = activity.metadata?.result
  return <div className="coding-session-action">
    <span className="coding-session-action-label"><span data-i18n-ignore>{action}</span>{parameter && <code data-i18n-ignore>{parameter}</code>}</span>
    {result && <details className="coding-session-action-result"><summary>{t('Output')}</summary><pre data-i18n-ignore>{result}</pre></details>}
  </div>
}
