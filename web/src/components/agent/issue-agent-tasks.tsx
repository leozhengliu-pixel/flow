import { useEffect, useMemo, useRef, useState } from 'react'
import { Archive, Bot, MessageSquarePlus, RefreshCw, Send, Sparkles, Square } from 'lucide-react'
import type { BootstrapData, Issue, IssueUpdateInput } from '@/types/flow'
import { PersonPicker } from '@/components/issue/core-property-pickers'
import {
  canDelegateTo,
  getApplicationTask,
  listApplicationTasks,
  replyApplicationTask,
  type ApplicationTask,
  type ApplicationActivity,
} from '@/lib/application-agents'
import { ClientStorage } from '@/lib/client-storage'
import { AgentRichText } from './agent-rich-text'
import { AgentSessionExternalUrlsButton } from './agent-session-external-urls-button'
import { externalUrlsFromActivities } from './agent-session-external-urls'
import { addSelectionAsAgentContext, AGENT_PANEL_OPEN_EVENT, isAgentPanelOpen } from './add-selection-as-agent-context'
import { useLinearAgentItems } from '@/hooks/use-linear-agent-items'
import { useI18n } from '@/i18n/i18n'
import './application-agents.css'

const ARCHIVED_TASKS_KEY = 'flow:agent-task-archived'

function readArchivedTaskIds(): Set<string> {
  const stored = ClientStorage.get<string[]>(ARCHIVED_TASKS_KEY) ?? []
  return new Set(stored.filter((id): id is string => typeof id === 'string'))
}

function writeArchivedTaskIds(ids: Set<string>) {
  ClientStorage.set(ARCHIVED_TASKS_KEY, [...ids])
}

export function IssueAgentPicker({
  issue,
  data,
  onUpdate,
}: {
  issue: Issue
  data: BootstrapData
  onUpdate: (input: IssueUpdateInput) => Promise<void>
}) {
  const { t } = useI18n()
  const users = data.users.filter(user => canDelegateTo(user, issue.team.id))
  if (!users.length && !issue.delegate) return null
  if (data.viewerRole === 'guest' && data.workspaceSettings.preventGuestAgents) return null
  return (
    <div className="core-property-picker">
      <PersonPicker
        label="Agent"
        ariaLabel={t('Delegate to agent')}
        people={users.map(user => ({ ...user, label: user.displayName }))}
        selectedId={issue.delegate?.id}
        onChange={delegateId => onUpdate({ delegateId })}
        emptyOptionLabel="No agent"
        emptyTriggerLabel="Delegate to agent"
        searchPlaceholder="Find agent…"
        triggerClassName="core-property-trigger"
      />
    </div>
  )
}

/** LS-0038 AgentSessionActivities — issue/document/project agent session activity list. */
export function IssueAgentTasks({
  issue,
  data,
  resourceType = 'issue',
}: {
  issue: Pick<Issue, 'id' | 'delegate' | 'agentSessionId'>
  data: BootstrapData
  resourceType?: 'issue' | 'document' | 'project'
}) {
  const { t } = useI18n()
  const [tasks, setTasks] = useState<ApplicationTask[]>([])
  const [revision, setRevision] = useState(0)
  const [error, setError] = useState('')
  const [archivedIds, setArchivedIds] = useState<Set<string>>(() => readArchivedTaskIds())
  const [showArchived, setShowArchived] = useState(false)
  const hasApplications = Boolean(issue.delegate || data.users.some(user => user.app))

  useEffect(() => {
    if (!hasApplications) return
    const abort = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    const load = async () => {
      let delay = 5000
      try {
        const next = await listApplicationTasks(data.workspace.urlKey, issue.id, abort.signal, resourceType)
        if (!abort.signal.aborted) {
          setTasks(next)
          setError('')
          delay = next.some(task => task.status === 'pending' || task.status === 'active') ? 1000 : 5000
        }
      } catch (loadError) {
        if (!abort.signal.aborted) setError(String(loadError instanceof Error ? loadError.message : loadError))
      } finally {
        if (!abort.signal.aborted) timer = setTimeout(load, delay)
      }
    }
    void load()
    return () => {
      abort.abort()
      clearTimeout(timer)
    }
  }, [data.workspace.urlKey, issue.id, issue.agentSessionId, revision, hasApplications, resourceType])

  const visibleTasks = tasks.filter(task => (showArchived ? true : !archivedIds.has(task.id)))
  const archivedCount = tasks.filter(task => archivedIds.has(task.id)).length

  if (!issue.delegate && !tasks.length && !error) return null

  return (
    <section className="issue-agent-tasks" aria-label={t('Agent sessions')}>
      <header>
        <Bot size={16} />
        <strong>{t('Agent sessions')}</strong>
        {archivedCount > 0 && (
          <button
            aria-pressed={showArchived}
            className="issue-agent-archived-toggle"
            onClick={() => setShowArchived(value => !value)}
            type="button"
          >
            {showArchived ? t('Hide archived') : t('Show archived')} ({archivedCount})
          </button>
        )}
        <button aria-label={t('Refresh')} onClick={() => setRevision(value => value + 1)} type="button">
          <RefreshCw size={14} />
        </button>
      </header>
      {error && <p role="alert">{error}</p>}
      {visibleTasks.map(task => (
        <AgentSessionActivities
          key={task.id}
          archived={archivedIds.has(task.id)}
          issueId={issue.id}
          name={data.users.find(user => user.id === task.appUserId)?.displayName ?? t('Application')}
          onArchivedChange={next => {
            setArchivedIds(current => {
              const copy = new Set(current)
              if (next) copy.add(task.id)
              else copy.delete(task.id)
              writeArchivedTaskIds(copy)
              return copy
            })
          }}
          onChanged={() => setRevision(value => value + 1)}
          task={task}
          workspace={data.workspace.urlKey}
        />
      ))}
    </section>
  )
}

export function AgentSessionActivities({
  task,
  workspace,
  name,
  issueId,
  archived,
  onArchivedChange,
  onChanged,
}: {
  task: ApplicationTask
  workspace: string
  name: string
  issueId: string
  archived: boolean
  onArchivedChange: (archived: boolean) => void
  onChanged: () => void
}) {
  const { t } = useI18n()
  const [activities, setActivities] = useState<ApplicationActivity[]>([])
  const [reply, setReply] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const cursor = useRef({ key: '', after: '' })

  useEffect(() => {
    const abort = new AbortController()
    const key = `${workspace}:${task.id}`
    const load = async () => {
      const reset = cursor.current.key !== key
      let after = reset ? '' : cursor.current.after
      const items: ApplicationActivity[] = []
      for (;;) {
        const result = await getApplicationTask(workspace, task.id, after, abort.signal)
        items.push(...result.activities)
        if (result.activities.length) after = result.activities.at(-1)!.id
        if (result.activities.length < 100) break
      }
      if (!abort.signal.aborted) {
        cursor.current = { key, after }
        setActivities(current => (reset ? items : [...current, ...items]))
        setError('')
      }
    }
    void load().catch(loadError => {
      if (!abort.signal.aborted) setError(String(loadError.message ?? loadError))
    })
    return () => abort.abort()
  }, [workspace, task.id, task.version])

  const externalUrls = useMemo(() => externalUrlsFromActivities(activities), [activities])
  const items = useLinearAgentItems({
    tasks: [task],
    activitiesByTaskId: { [task.id]: activities },
  })

  const submit = async (type: 'prompt' | 'canceled' | 'retry', approve?: boolean) => {
    if (busy) return
    setBusy(true)
    try {
      await replyApplicationTask(workspace, task, type, reply, approve)
      setReply('')
      setError('')
      onChanged()
    } catch (submitError) {
      setError(String(submitError instanceof Error ? submitError.message : submitError))
    } finally {
      setBusy(false)
    }
  }

  const resolveWithAgent = () => {
    const prompt =
      task.prompt?.trim() ||
      activities.find(activity => activity.type === 'prompt' || activity.type === 'response')?.body ||
      t('Help resolve this agent session')
    if (isAgentPanelOpen()) {
      addSelectionAsAgentContext(prompt, { issueId, openIfClosed: false })
      return
    }
    window.dispatchEvent(
      new CustomEvent(AGENT_PANEL_OPEN_EVENT, {
        detail: { issueId, initialPrompt: prompt },
      }),
    )
  }

  const addToContext = () => {
    const body = activities
      .filter(activity => activity.type === 'response' || activity.type === 'output')
      .map(activity => activity.body)
      .filter(Boolean)
      .join('\n\n')
    const textBody = body || task.prompt || ''
    if (isAgentPanelOpen()) {
      addSelectionAsAgentContext(textBody, { issueId, openIfClosed: false })
      return
    }
    window.dispatchEvent(
      new CustomEvent(AGENT_PANEL_OPEN_EVENT, {
        detail: { issueId, initialPrompt: textBody },
      }),
    )
  }

  return (
    <article className="issue-agent-task" data-archived={archived || undefined} data-agent-item-count={items.length}>
      <header>
        <span>{name}</span>
        {archived && <span className="issue-agent-task-archived">{t('Session archived')}</span>}
        <span className="issue-agent-task-status">
          {t(
            (
              {
                pending: 'Pending',
                active: 'Working',
                awaitingInput: 'Needs input',
                complete: 'Completed',
                error: 'Failed',
                canceled: 'Canceled',
              } as Record<string, string>
            )[task.status],
          )}
        </span>
        <AgentSessionExternalUrlsButton urls={externalUrls} />
        <button
          aria-label={t('Resolve with agent')}
          disabled={busy}
          onClick={resolveWithAgent}
          title={t('Resolve with agent')}
          type="button"
        >
          <Sparkles size={12} />
        </button>
        <button
          aria-label={t('Add to context')}
          disabled={busy}
          onClick={addToContext}
          title={t('Add to context')}
          type="button"
        >
          <MessageSquarePlus size={12} />
        </button>
        <button
          aria-label={archived ? t('Unarchive session') : t('Archive session')}
          disabled={busy}
          onClick={() => onArchivedChange(!archived)}
          title={archived ? t('Unarchive session') : t('Archive session')}
          type="button"
        >
          <Archive size={12} />
        </button>
        {['pending', 'active', 'awaitingInput'].includes(task.status) ? (
          <button aria-label={t('Stop agent')} disabled={busy} onClick={() => void submit('canceled')} type="button">
            <Square size={12} />
          </button>
        ) : (
          <button aria-label={t('Retry agent')} disabled={busy} onClick={() => void submit('retry')} type="button">
            <RefreshCw size={12} />
          </button>
        )}
      </header>
      <div className="issue-agent-activities">
        {activities
          .filter(
            activity =>
              activity.type !== 'output' || !activities.some(item => item.type === 'response' && item.id > activity.id),
          )
          .map(activity => (
            <div key={activity.id} data-type={activity.type}>
              {activity.type === 'thought' ? (
                <details>
                  <summary>{t('Progress')}</summary>
                  <AgentRichText className="issue-agent-markdown" content={activity.body} />
                </details>
              ) : (
                <>
                  <small>
                    {t(
                      (
                        {
                          response: 'Response',
                          output: 'Working',
                          action: 'Action',
                          elicitation: 'Needs input',
                          prompt: 'Reply',
                          retry: 'Retry',
                          error: 'Error',
                          canceled: 'Canceled',
                        } as Record<string, string>
                      )[activity.type] ?? activity.type,
                    )}
                  </small>
                  <AgentRichText className="issue-agent-markdown" content={activity.body} />
                  {activity.url && (
                    <a href={activity.url} rel="noreferrer" target="_blank">
                      {t('Open result')}
                    </a>
                  )}
                </>
              )}
            </div>
          ))}
      </div>
      {task.status === 'awaitingInput' &&
        (task.pendingTool ? (
          <div className="issue-agent-approval">
            <button disabled={busy} onClick={() => void submit('prompt', false)} type="button">
              {t('Reject')}
            </button>
            <button disabled={busy} onClick={() => void submit('prompt', true)} type="button">
              {t('Approve')}
            </button>
          </div>
        ) : (
          <form
            onSubmit={event => {
              event.preventDefault()
              void submit('prompt')
            }}
          >
            <textarea aria-label={t('Reply to agent')} onChange={event => setReply(event.target.value)} value={reply} />
            <button aria-label={t('Send reply')} disabled={busy || !reply.trim()} type="submit">
              <Send size={14} />
            </button>
          </form>
        ))}
      {error && <p role="alert">{error}</p>}
    </article>
  )
}
