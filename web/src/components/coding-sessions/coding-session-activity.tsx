import { useEffect, useMemo, useRef, useState } from 'react'
import { AlertCircle, ChevronRight, ExternalLink, Lightbulb, MessageSquare, Terminal } from 'lucide-react'
import { AGENT_TASK_ACTIVITY_EVENT, getApplicationTask, replyApplicationTask, type ApplicationActivity, type ApplicationTask } from '@/lib/application-agents'
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
  const { t } = useI18n()
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
  const list = <ol className="coding-session-activity" aria-label={t('Session activity')}>
    {items.map(activity => {
      const options = (activity.signal === 'select' ? activity.signalMetadata?.options ?? [] : []).map(option => ({ label: option.label || option.value, value: option.value }))
      const answered = answers.get(activity.id)
      return <li key={activity.id} className="coding-session-activity-item" data-type={activity.type}>
        <span className="coding-session-activity-icon" aria-hidden="true"><ActivityIcon type={activity.type}/></span>
        <div className="coding-session-activity-body">
          {activity.type === 'prompt' && <><strong data-i18n-ignore>{userName(activity.actorId)}</strong><p data-i18n-ignore>{activity.body}</p></>}
          {activity.type === 'thought' && <p className="coding-session-thought" data-i18n-ignore>{activity.body}</p>}
          {activity.type === 'action' && <ActionRow activity={activity}/>}
          {activity.type === 'response' && <AgentAnswerText className="coding-session-markdown" data={data} markdown={activity.body}/>}
          {activity.type === 'error' && <p className="coding-session-activity-error" role="alert" data-i18n-ignore>{activity.body}</p>}
          {activity.type === 'elicitation' && <>
            <AgentAnswerText className="coding-session-markdown" data={data} markdown={activity.body}/>
            {options.length > 0 && <AgentChoicePills choices={options} disabled={busy || pending?.id !== activity.id} onSelect={choice => void answer(choice.value)} selected={answered}/>}
            {activity.signal === 'auth' && !answered && activity.signalMetadata?.url && /^https?:\/\//i.test(activity.signalMetadata.url) && <a className="coding-session-link" href={activity.signalMetadata.url} target="_blank" rel="noreferrer"><ExternalLink size={13} aria-hidden="true"/>{t('Link account')}</a>}
          </>}
        </div>
      </li>
    })}
  </ol>
  const steps = items.length
  return <div className="coding-session-activity-block">
    {mode === 'steps'
      ? <button type="button" className="coding-session-disclosure" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>
        <ChevronRight size={13} aria-hidden="true" data-open={expanded || undefined}/>{t(expanded ? 'Hide steps' : 'Show {count} steps').replace('{count}', String(steps))}
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
    <span className="coding-session-action-label"><strong data-i18n-ignore>{action}</strong>{parameter && <code data-i18n-ignore>{parameter}</code>}</span>
    {result && <details className="coding-session-action-result"><summary>{t('Output')}</summary><pre data-i18n-ignore>{result}</pre></details>}
  </div>
}

function ActivityIcon({ type }: { type: string }) {
  if (type === 'action') return <Terminal size={13}/>
  if (type === 'thought') return <Lightbulb size={13}/>
  if (type === 'error') return <AlertCircle size={13}/>
  return <MessageSquare size={13}/>
}
