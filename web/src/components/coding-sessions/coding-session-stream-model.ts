/**
 * Pure rules of the coding session stream, reimplemented from the reference app's behaviour (see the session UI spec):
 * which activities show, how adjacent thoughts and actions fold into one "Worked for …" group, the turn layout with
 * time markers, the plan's current step, duration copy and the per-session message queue. No React here.
 */
import type { ApplicationActivity } from '@/lib/application-agents'
import type { AgentMessageAttachment, CodeReview } from '@/types/flow'
import type { CodingCommit, CodingPlanStep, CodingSession, SessionPullRequest } from '@/types/coding-sessions'

const SHOWN_TYPES = new Set(['prompt', 'thought', 'action', 'response', 'elicitation', 'error'])

/** One action row; `loading` while it is the live step of a generating session. */
export type StreamAction = { kind: 'action'; activity: ApplicationActivity; loading: boolean }
export type StreamReasoning = { kind: 'reasoning'; activity: ApplicationActivity }
/** "Used N tools": a run of consecutive actions folded inside a long group. */
export type StreamTools = { kind: 'tools'; id: string; actions: StreamAction[] }
export type GroupChild = StreamAction | StreamReasoning | StreamTools

export type StreamItem =
  | { kind: 'prompt'; activity: ApplicationActivity; stop: boolean }
  | { kind: 'text'; activity: ApplicationActivity }
  | { kind: 'auth'; activity: ApplicationActivity }
  | { kind: 'error'; activity: ApplicationActivity }
  | StreamAction
  | StreamReasoning
  | { kind: 'group'; id: string; children: GroupChild[]; loading: boolean; startedAt?: string; endedAt?: string; /** The live action's label while loading. */ live?: ApplicationActivity; /** The latest thought, shown under a live group. */ promoted?: string }
  | { kind: 'thinking' }

export type StreamTurn = { id: string; prompt?: Extract<StreamItem, { kind: 'prompt' }>; items: StreamItem[]; at?: string; showTime: boolean }

/** The session is producing output (the trailing step is live). */
export const isCodingSessionGenerating = (session: Pick<CodingSession, 'state' | 'phase'>) =>
  (session.state === 'pending' || session.state === 'active') && session.phase !== 'awaitingApproval'

/**
 * The activities the stream shows: known types, not deleted, ephemeral ones only while they are the latest non-prompt
 * activity (a live step replaced in place), and no thought that merely repeats the response after it.
 */
export function visibleActivities(activities: readonly ApplicationActivity[]): ApplicationActivity[] {
  const deleted = new Set(activities.filter(activity => activity.type === 'deleted').map(activity => activity.metadata?.activityId))
  const shown = activities.filter(activity => SHOWN_TYPES.has(activity.type) && !deleted.has(activity.id))
  const latestAgent = [...shown].reverse().find(activity => activity.type !== 'prompt')
  return shown.filter((activity, index) => {
    if (activity.ephemeral && activity !== latestAgent) return false
    const next = shown[index + 1]
    return !(activity.type === 'thought' && next?.type === 'response' && next.body.trim() === activity.body.trim())
  })
}

const MAX_GROUP_CHILDREN = 20
const PROMOTED_LENGTH = 280

function foldTools(children: GroupChild[]): GroupChild[] {
  if (children.length <= MAX_GROUP_CHILDREN) return children
  const folded: GroupChild[] = []
  let run: StreamAction[] = []
  const flush = () => {
    if (run.length > 1) folded.push({ kind: 'tools', id: `tools-${run[0].activity.id}`, actions: run })
    else folded.push(...run)
    run = []
  }
  for (const child of children) {
    if (child.kind === 'action' && !child.loading) run.push(child)
    else { flush(); folded.push(child) }
  }
  flush()
  return folded
}

/**
 * Activities → stream items: thoughts become reasoning, actions action rows, responses and questions text, auth
 * questions a muted line, errors error cards. Consecutive reasoning and actions form a group when there are two or
 * more, when one is still loading, or for a lone thought right before a text. While the session generates the trailing
 * step is loading, and a "Thinking…" row stands in until the first step arrives.
 */
export function buildStreamItems(activities: readonly ApplicationActivity[], generating: boolean): StreamItem[] {
  const shown = visibleActivities(activities)
  const items: StreamItem[] = []
  let run: (StreamAction | StreamReasoning)[] = []
  const last = shown.at(-1)
  const flush = (nextIsText: boolean) => {
    if (!run.length) return
    const loading = run.some(child => child.kind === 'action' && child.loading) || (generating && run.at(-1)!.activity === last)
    const lone = run.length === 1 && !loading && !(run[0].kind === 'reasoning' && nextIsText)
    if (lone) items.push(run[0])
    else {
      const live = loading ? [...run].reverse().find((child): child is StreamAction => child.kind === 'action' && child.activity === last)?.activity : undefined
      const latestThought = [...run].reverse().find(child => child.kind === 'reasoning')?.activity.body.trim()
      items.push({
        kind: 'group', id: `group-${run[0].activity.id}`, children: foldTools(run), loading,
        startedAt: run[0].activity.createdAt || undefined, endedAt: run.at(-1)!.activity.createdAt || undefined, live,
        promoted: loading && latestThought ? (latestThought.length > PROMOTED_LENGTH ? `${latestThought.slice(0, PROMOTED_LENGTH - 1)}…` : latestThought) : undefined,
      })
    }
    run = []
  }
  for (const activity of shown) {
    if (activity.type === 'thought') { run.push({ kind: 'reasoning', activity }); continue }
    if (activity.type === 'action') { run.push({ kind: 'action', activity, loading: generating && activity === last }); continue }
    const isText = activity.type === 'response' || (activity.type === 'elicitation' && activity.signal !== 'auth')
    flush(isText)
    if (activity.type === 'prompt') items.push({ kind: 'prompt', activity, stop: activity.signal === 'stop' })
    else if (activity.type === 'error') items.push({ kind: 'error', activity })
    else if (activity.type === 'elicitation' && activity.signal === 'auth') items.push({ kind: 'auth', activity })
    else items.push({ kind: 'text', activity })
  }
  flush(false)
  const tail = items.at(-1)
  if (generating && !(tail?.kind === 'group' && tail.loading)) items.push({ kind: 'thinking' })
  return items
}

const HALF_DAY = 12 * 60 * 60 * 1000

/** Items → turns (a prompt and the agent's items after it). A time marker shows before the first turn and whenever 12h passed or the day changed. */
export function buildStreamTurns(items: readonly StreamItem[]): StreamTurn[] {
  const turns: StreamTurn[] = []
  for (const item of items) {
    if (item.kind === 'prompt' || !turns.length) {
      const at = item.kind === 'prompt' || item.kind === 'text' || item.kind === 'error' || item.kind === 'auth' || item.kind === 'action' || item.kind === 'reasoning' ? item.activity.createdAt : item.kind === 'group' ? item.startedAt : undefined
      turns.push({ id: item.kind === 'prompt' ? item.activity.id : 'start', prompt: item.kind === 'prompt' ? item : undefined, items: item.kind === 'prompt' ? [] : [item], at: at || undefined, showTime: false })
    } else turns.at(-1)!.items.push(item)
  }
  let previous: number | undefined
  for (const turn of turns) {
    const time = turn.at ? Date.parse(turn.at) : Number.NaN
    if (Number.isNaN(time)) { turn.showTime = previous === undefined && turn === turns[0] && Boolean(turn.at); continue }
    turn.showTime = previous === undefined || time - previous > HALF_DAY || new Date(time).toDateString() !== new Date(previous).toDateString()
    previous = time
  }
  return turns
}

/**
 * The session's commits per turn (the badge goes after the turn's last agent item): keyed by the turn's prompt activity;
 * the first turn also takes the initial turn id (`turn_<session>`), and commits of turns not in the stream go to the last.
 */
export function commitsByTurn(turns: readonly StreamTurn[], session: Pick<CodingSession, 'id' | 'commitsByTurnActivityId'>): Map<string, CodingCommit[]> {
  const result = new Map<string, CodingCommit[]>()
  const entries = Object.entries(session.commitsByTurnActivityId ?? {}).filter(([, commits]) => commits.length)
  if (!turns.length || !entries.length) return result
  const keys = new Map<string, string>()
  turns.forEach((turn, index) => {
    if (turn.prompt) keys.set(turn.prompt.activity.id, turn.id)
    if (index === 0) keys.set(`turn_${session.id}`, turn.id)
  })
  for (const [key, commits] of entries) {
    const turn = keys.get(key) ?? turns.at(-1)!.id
    result.set(turn, [...(result.get(turn) ?? []), ...commits])
  }
  return result
}

export type DurationUnit = 'second' | 'minute' | 'hour' | 'day' | 'week'

/**
 * The reference app's duration copy: under a minute in seconds, under an hour in rounded minutes, under 8 hours in
 * hours (rounded up from 40 minutes past), then whole hours, days and weeks.
 */
export function durationParts(ms: number): { count: number; unit: DurationUnit } {
  const seconds = Math.max(1, Math.round(ms / 1000))
  if (seconds < 60) return { count: seconds, unit: 'second' }
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return { count: minutes, unit: 'minute' }
  if (seconds < 8 * 3600) {
    const hours = Math.floor(seconds / 3600)
    return { count: seconds - hours * 3600 >= 40 * 60 ? hours + 1 : hours, unit: 'hour' }
  }
  if (seconds < 86400) return { count: Math.round(seconds / 3600), unit: 'hour' }
  const days = Math.round(seconds / 86400)
  if (days < 7) return { count: days, unit: 'day' }
  return { count: Math.round(days / 7), unit: 'week' }
}

/** "11 seconds", "3 minutes", "1 hour" through a translator ("{count} seconds" → "{count} 秒"). */
export function formatDuration(ms: number, t: (source: string) => string): string {
  const { count, unit } = durationParts(ms)
  return t(count === 1 ? `{count} ${unit}` : `{count} ${unit}s`).replace('{count}', String(count))
}

/** "Worked for 11 seconds", or "Worked for a while" without timestamps. */
export function workedForLabel(startedAt: string | undefined, endedAt: string | undefined, t: (source: string) => string): string {
  const start = startedAt ? Date.parse(startedAt) : Number.NaN
  const end = endedAt ? Date.parse(endedAt) : Number.NaN
  if (Number.isNaN(start) || Number.isNaN(end)) return t('Worked for a while')
  return t('Worked for {duration}').replace('{duration}', formatDuration(Math.max(1000, end - start), t))
}

/** The plan step the collapsed pill shows: the first in progress, else the first pending, else the last. */
export function currentPlanStep(plan: readonly CodingPlanStep[]): { index: number; step: CodingPlanStep } | undefined {
  if (!plan.length) return undefined
  let index = plan.findIndex(step => step.status === 'inProgress')
  if (index < 0) index = plan.findIndex(step => step.status === 'pending')
  if (index < 0) index = plan.length - 1
  return { index, step: plan[index] }
}

/** Inline select options: one option, or at most two short ones that are not repository URLs; anything else is a dropdown. */
export function selectOptionsInline(options: readonly { label?: string; value: string }[]): boolean {
  if (options.length === 1) return true
  if (options.length > 2) return false
  return options.every(option => {
    const text = option.label || option.value
    return text.length < 30 && !/^https?:\/\/(?:www\.)?(?:github|gitlab)\./i.test(text)
  })
}

/** The open question the composer answers: the latest elicitation that no prompt answered yet. */
export function pendingElicitation(activities: readonly ApplicationActivity[]): ApplicationActivity | undefined {
  const shown = visibleActivities(activities)
  for (let index = shown.length - 1; index >= 0; index--) {
    const activity = shown[index]
    if (activity.type === 'prompt') return undefined
    if (activity.type === 'elicitation') return activity
  }
  return undefined
}

/** A stop request is pending: the latest prompt is a stop signal and the session has not stopped yet. */
export const stopRequested = (session: Pick<CodingSession, 'state' | 'stopRequested'>) => session.state === 'stopping' || Boolean(session.stopRequested && (session.state === 'active' || session.state === 'pending'))

export type QueuedMessage = { id: string; text: string; files?: AgentMessageAttachment[]; createdAt: string }

const queueKey = (workspace: string, sessionId: string) => `flow:coding-sessions:queue:${workspace}:${sessionId}`

/** Messages queued while the agent works (kept per session in this browser until sent). */
export function readQueuedMessages(workspace: string, sessionId: string): QueuedMessage[] {
  try {
    const raw = localStorage.getItem(queueKey(workspace, sessionId))
    const parsed: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed) ? parsed.filter((item): item is QueuedMessage => Boolean(item) && typeof item.id === 'string' && typeof item.text === 'string') : []
  } catch { return [] }
}

export function writeQueuedMessages(workspace: string, sessionId: string, messages: readonly QueuedMessage[]) {
  try {
    if (messages.length) localStorage.setItem(queueKey(workspace, sessionId), JSON.stringify(messages))
    else localStorage.removeItem(queueKey(workspace, sessionId))
  } catch { /* the queue still works for this page view */ }
}

/** "Today 11:00 PM", "Yesterday 3:04 PM", "Oct 18 at 11:00 PM". */
export function streamTimeLabel(value: string, locale: string, t: (source: string) => string, now = new Date()): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const time = date.toLocaleTimeString(locale, { hour: 'numeric', minute: '2-digit' })
  const yesterday = new Date(now)
  yesterday.setDate(now.getDate() - 1)
  if (date.toDateString() === now.toDateString()) return `${t('Today')} ${time}`
  if (date.toDateString() === yesterday.toDateString()) return `${t('Yesterday')} ${time}`
  const day = date.toLocaleDateString(locale, { month: 'short', day: 'numeric', ...(date.getFullYear() === now.getFullYear() ? {} : { year: 'numeric' }) })
  return t('{date} at {time}').replace('{date}', day).replace('{time}', time)
}


export type ErrorVariant = 'signingKey' | 'codeAccess' | 'connect' | 'awaiting' | 'generic'

/** The variant of an error card for a session error code. */
export function errorVariant(code: string | undefined, viewerIsCreator: boolean): ErrorVariant {
  switch (code) {
    case 'missingCommitSigningKey': return 'signingKey'
    case 'noCodeAccess': case 'noGitHubConnection': case 'noGitLabConnection': return 'codeAccess'
    case 'requiresGithubConnection': case 'requiresGitlabConnection': case 'personalAccessDenied': return viewerIsCreator ? 'connect' : 'awaiting'
    default: return 'generic'
  }
}


/** The PR card's states (the reference app has a merge-queue "Merging" state too; Flow's code hosts report none). */
export type PullRequestCardStatus = 'draft' | 'open' | 'inReview' | 'approved' | 'merged' | 'closed'
export const PULL_REQUEST_CARD_LABELS: Record<PullRequestCardStatus, string> = { draft: 'Draft', open: 'Open', inReview: 'In review', approved: 'Approved', merged: 'Merged', closed: 'Closed' }

/** Merged/closed from the PR itself, draft before review, then the synced review's state (in review, approved). */
export function pullRequestCardStatus(pr: Pick<SessionPullRequest, 'state' | 'draft'>, review?: Pick<CodeReview, 'status' | 'draft'>): PullRequestCardStatus {
  if (pr.state === 'merged' || review?.status === 'merged') return 'merged'
  if (pr.state === 'closed' || review?.status === 'closed') return 'closed'
  if (pr.draft || review?.draft) return 'draft'
  if (review?.status === 'approved') return 'approved'
  if (review?.status === 'inReview') return 'inReview'
  return 'open'
}
