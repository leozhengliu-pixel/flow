import { useCallback, useRef, useState, type ReactNode } from 'react'
import { useI18n } from '@/i18n/i18n'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { ApiError } from '@/lib/api-client'
import { archiveCodingSession, fetchCodingEnvironments, fetchCodingSessions, startIssueCodingSession } from '@/lib/coding-sessions-api'
import { toast } from '@/lib/toast'
import type { BootstrapData, Issue, IssueUpdateInput, User } from '@/types/flow'
import type { CodingEnvironment, CodingSession } from '@/types/coding-sessions'
import { notifyCodingSessionChanged } from './coding-session-events'
import { codingErrorInfo, isCodingErrorCode, isCodingSessionTerminal, readLastUsedEnvironment, suggestEnvironment, writeLastUsedEnvironment } from './coding-session-model'
import { CodingEnvironmentPickerDialog } from './start-coding-session'
import { useCodingAvailability } from './use-coding-sessions'

type StartIssue = Pick<Issue, 'id' | 'identifier'> & { team: Pick<Issue['team'], 'id'>; delegate?: Pick<User, 'id' | 'displayName' | 'name' | 'builtinAgent'> }
export type StartOptions = {
  prompt?: string
  /** Delegation: start only when an environment exists (otherwise the agent handles the issue as a normal delegation). */
  requireEnvironment?: boolean
}

type Translate = (key: string) => string

/** A failed start as a toast with the error's next action (connect GitHub, upload a signing key, add credits…). */
export function toastCodingStartError(error: unknown, workspace: string, t: Translate) {
  const code = error instanceof ApiError ? error.code : undefined
  if (isCodingErrorCode(code)) {
    const info = codingErrorInfo(code, { workspace })
    const link = info.actions.find(action => action.kind === 'link')
    toast.error(t(info.title), { description: t(info.message), action: link && link.kind === 'link' ? { label: t(link.label), href: link.href } : undefined })
    return
  }
  toast.error(error instanceof Error ? error.message : t('Could not start coding session'))
}

/**
 * Starts a coding session for an issue (§15): the environment is picked automatically from the repository suggestion
 * (single environment, the issue's linked PRs, the team's last-used one); when that stays ambiguous a picker opens.
 * Returns `start` plus the picker element to render.
 */
export function useIssueCodingSessionStarter(issue: StartIssue, data: BootstrapData, onStarted?: (session: CodingSession) => void) {
  const { t } = useI18n()
  const available = useCodingAvailability(data)
  const [picker, setPicker] = useState<{ environments: CodingEnvironment[]; suggested?: CodingEnvironment; prompt?: string }>()
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const workspace = data.workspace.urlKey
  const launch = useCallback(async (environmentId: string | undefined, prompt?: string) => {
    if (busyRef.current) return
    busyRef.current = true; setBusy(true)
    try {
      const session = await startIssueCodingSession(issue.id, { environmentId, prompt: prompt?.trim() || undefined })
      if (environmentId) writeLastUsedEnvironment(workspace, issue.team.id, environmentId)
      notifyCodingSessionChanged({ sessionId: session.id, agentSessionId: session.agentSessionId, issueId: issue.id })
      toast.success(t('Started coding session'), { description: issue.identifier })
      setPicker(undefined)
      onStarted?.(session)
      return session
    } catch (error) {
      toastCodingStartError(error, workspace, t)
    } finally { busyRef.current = false; setBusy(false) }
  }, [issue.id, issue.identifier, issue.team.id, workspace, t, onStarted])
  const start = useCallback(async (options: StartOptions = {}) => {
    let environments: CodingEnvironment[]
    try { environments = await fetchCodingEnvironments() }
    catch (error) {
      if (options.requireEnvironment) return
      toastCodingStartError(error, workspace, t); return
    }
    const suggestion = suggestEnvironment(Array.isArray(environments) ? environments : [], { issue, reviews: data.reviews ?? [], lastUsedId: readLastUsedEnvironment(workspace, issue.team.id) })
    if (suggestion.kind === 'none') {
      if (options.requireEnvironment) return
      return launch(undefined, options.prompt)
    }
    if (suggestion.kind === 'single') return launch(suggestion.environment.id, options.prompt)
    setPicker({ environments: suggestion.environments, suggested: suggestion.suggested, prompt: options.prompt })
  }, [data.reviews, issue, workspace, t, launch])
  const element: ReactNode = picker ? <CodingEnvironmentPickerDialog environments={picker.environments} suggested={picker.suggested} initialPrompt={picker.prompt} busy={busy} onCancel={() => setPicker(undefined)} onStart={(environmentId: string, prompt: string) => void launch(environmentId, prompt)}/> : null
  return { available, start, element, busy }
}

/**
 * Delegating an issue to the built-in Flow agent starts a coding session for it when coding sessions are on and an
 * environment exists. Removing that agent from the issue's delegate row ("No agent", `promptArchive`) drops the
 * delegate at once and then asks "Archive session with <agent>?" for its unarchived sessions, running or finished:
 * "Skip" leaves them (a running session keeps going), "Archive session" archives them (which ends a running session
 * and closes its open pull requests). Removing it through the unassign flow's "Remove <agent> too?" dialog, which
 * already said it would archive the working session, archives the running ones without a second question.
 * Wraps the delegate picker's `onUpdate`; other updates pass straight through.
 */
export function useDelegateCodingSession(issue: StartIssue, data: BootstrapData, onUpdate: (input: IssueUpdateInput) => void | Promise<void>) {
  const { t } = useI18n()
  const starter = useIssueCodingSessionStarter(issue, data)
  const { available, start } = starter
  const previousDelegate = issue.delegate
  const update = useCallback(async (request: { assigneeId?: string; delegateId?: string; promptArchive?: boolean }) => {
    const { promptArchive, ...input } = request
    await onUpdate(input)
    if (!available || input.delegateId === undefined) return
    if (input.delegateId === '') {
      if (!previousDelegate?.builtinAgent) return
      if (promptArchive) await offerArchive(issue, previousDelegate.displayName || previousDelegate.name, t)
      else await archiveWorkingSessions(issue, t)
      return
    }
    const delegate = data.users.find(user => user.id === input.delegateId)
    if (!delegate?.builtinAgent) return
    await start({ requireEnvironment: true })
  }, [onUpdate, available, data.users, start, previousDelegate, issue, t])
  return { onUpdate: update, element: starter.element }
}

/**
 * After the agent left the issue its still-running sessions end with it, without a second question: the unassign
 * flow's "Remove agent" already said "remove it and archive its working session". Finished sessions stay as they are.
 */
async function archiveWorkingSessions(issue: StartIssue, t: Translate) {
  let sessions: CodingSession[]
  try { sessions = (await fetchCodingSessions({ issueId: issue.id })).filter(session => !session.archivedAt && !isCodingSessionTerminal(session)) }
  catch { return }
  await archiveSessions(issue, sessions, t)
}

async function archiveSessions(issue: StartIssue, sessions: CodingSession[], t: Translate) {
  for (const session of sessions) {
    try {
      const archived = await archiveCodingSession(session.id)
      notifyCodingSessionChanged({ sessionId: archived.id, agentSessionId: archived.agentSessionId, issueId: issue.id })
    } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not archive session')) }
  }
}

/** The reference app's dialog after "No agent" on the delegate row: archive the agent's unarchived sessions, or skip. */
async function offerArchive(issue: StartIssue, agentName: string, t: Translate) {
  let sessions: CodingSession[]
  try { sessions = (await fetchCodingSessions({ issueId: issue.id })).filter(session => !session.archivedAt) }
  catch { return }
  if (!sessions.length) return
  const confirmed = await confirmAction(t('Archive session with {name}?').replace('{name}', agentName), {
    description: t('{name} has been removed from this issue. Do you want to archive the agent session? It can be restored at any time.').replace('{name}', agentName),
    confirmLabel: t('Archive session'), cancelLabel: t('Skip'), danger: false,
  })
  if (confirmed) await archiveSessions(issue, sessions, t)
}
