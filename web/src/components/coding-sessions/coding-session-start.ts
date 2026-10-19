import { createElement, useCallback, useRef, useState, type ReactNode } from 'react'
import { useI18n } from '@/i18n/i18n'
import { ApiError } from '@/lib/api-client'
import { fetchCodingEnvironments, startIssueCodingSession } from '@/lib/coding-sessions-api'
import { toast } from '@/lib/toast'
import type { BootstrapData, Issue, IssueUpdateInput } from '@/types/flow'
import type { CodingEnvironment, CodingSession } from '@/types/coding-sessions'
import { notifyCodingSessionChanged } from './coding-session-events'
import { codingErrorInfo, isCodingErrorCode, readLastUsedEnvironment, suggestEnvironment, writeLastUsedEnvironment } from './coding-session-model'
import { CodingEnvironmentPickerDialog } from './start-coding-session'
import { useCodingAvailability } from './use-coding-sessions'

type StartIssue = Pick<Issue, 'id' | 'identifier'> & { team: Pick<Issue['team'], 'id'> }
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
  const element: ReactNode = picker ? createElement(CodingEnvironmentPickerDialog, { environments: picker.environments, suggested: picker.suggested, initialPrompt: picker.prompt, busy, onCancel: () => setPicker(undefined), onStart: (environmentId: string, prompt: string) => void launch(environmentId, prompt) }) : null
  return { available, start, element, busy }
}

/**
 * Delegating an issue to the built-in Flow agent starts a coding session for it when coding sessions are on and an
 * environment exists. Wraps the delegate picker's `onUpdate`; other updates pass straight through.
 */
export function useDelegateCodingSession(issue: StartIssue, data: BootstrapData, onUpdate: (input: IssueUpdateInput) => void | Promise<void>) {
  const starter = useIssueCodingSessionStarter(issue, data)
  const { available, start } = starter
  const update = useCallback(async (input: { assigneeId?: string; delegateId?: string }) => {
    await onUpdate(input)
    if (!available || !input.delegateId) return
    const delegate = data.users.find(user => user.id === input.delegateId)
    if (!delegate?.builtinAgent) return
    await start({ requireEnvironment: true })
  }, [onUpdate, available, data.users, start])
  return { onUpdate: update, element: starter.element }
}
