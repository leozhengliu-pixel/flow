import { isAbsolute, relative } from 'node:path'
import type {
  ActionDetail,
  AgentPlanStep,
  CodingDiffSummary,
  CodingErrorCode,
  ErrorDetail,
  PlanStatus,
  RunnerEvent,
  RunnerEventType,
  UsageDetail,
} from '../protocol.js'

// Shared pieces of the harness stream → RunnerEvent mapping (§16).

export const RESULT_TAIL_BYTES = 4096

/** What a turn produced, independent of the harness. */
export interface TurnOutcome {
  /** Claude session_id / Codex thread_id / OpenCode sessionID, for resume. */
  harnessSessionId?: string
  model?: string
  /** The final assistant message (becomes the response and the commit summary). */
  finalText?: string
  error?: ErrorDetail
  usage?: UsageDetail
  /** The agent asked the user a question (ask_user): the turn ends awaiting input. */
  elicited: boolean
  /** A terminal event (result / turn.completed / final step) was seen. */
  completed: boolean
}

export interface StreamMapper {
  /** Maps one parsed JSONL record. Unknown records map to nothing. */
  map(record: unknown): RunnerEvent[]
  /** Called when the harness exits: flushes buffered text. */
  finish(): RunnerEvent[]
  outcome(): TurnOutcome
}

export interface MapperOptions {
  turnId?: string
  /** Repository root; absolute tool paths under it are made relative. */
  cwd?: string
  /** The configured model, for usage events when the stream does not name it. */
  model?: string
  newId?: () => string
  now?: () => Date
}

export type EventFields = Omit<RunnerEvent, 'id' | 'type' | 'createdAt' | 'turnId'>

export class EventFactory {
  private readonly turnId?: string
  private readonly newId: () => string
  private readonly now: () => Date

  constructor(options: MapperOptions) {
    this.turnId = options.turnId
    this.newId = options.newId ?? (() => crypto.randomUUID())
    this.now = options.now ?? (() => new Date())
  }

  make(type: RunnerEventType, fields: EventFields = {}): RunnerEvent {
    const event: RunnerEvent = { id: this.newId(), type, createdAt: this.now().toISOString() }
    if (this.turnId) event.turnId = this.turnId
    for (const [key, value] of Object.entries(fields)) {
      if (value !== undefined) (event as unknown as Record<string, unknown>)[key] = value
    }
    return event
  }

  thought(body: string, ephemeral = false): RunnerEvent {
    return this.make('thought', { body, ephemeral: ephemeral || undefined })
  }

  action(action: ActionDetail, ephemeral = false): RunnerEvent {
    return this.make('action', { action: compactAction(action), ephemeral: ephemeral || undefined })
  }

  response(body: string): RunnerEvent {
    return this.make('response', { body })
  }

  elicitation(body: string): RunnerEvent {
    return this.make('elicitation', { body })
  }

  error(error: ErrorDetail): RunnerEvent {
    return this.make('error', { error, body: error.message })
  }

  plan(plan: AgentPlanStep[]): RunnerEvent {
    return this.make('plan', { plan })
  }

  diff(diff: CodingDiffSummary[]): RunnerEvent {
    return this.make('diff', { diff })
  }

  usage(usage: UsageDetail): RunnerEvent {
    return this.make('usage', { usage })
  }

  status(phase: string, body?: string): RunnerEvent {
    return this.make('status', { phase, body })
  }
}

function compactAction(action: ActionDetail): ActionDetail {
  const out: ActionDetail = { action: action.action }
  if (action.parameter) out.parameter = action.parameter
  if (action.result !== undefined && action.result !== '') out.result = action.result
  if (action.tool) out.tool = action.tool
  if (action.exitCode !== undefined && action.exitCode !== null) out.exitCode = action.exitCode
  return out
}

/** The last `bytes` bytes of a string, cut on a character boundary, with a marker. */
export function tail(text: string, bytes = RESULT_TAIL_BYTES): string {
  const buffer = Buffer.from(text, 'utf8')
  if (buffer.length <= bytes) return text
  let start = buffer.length - bytes
  while (start < buffer.length && (buffer[start] & 0xc0) === 0x80) start++
  return '…' + buffer.subarray(start).toString('utf8')
}

export function truncate(text: string, max = 200): string {
  const single = text.replace(/\s+/g, ' ').trim()
  return single.length <= max ? single : single.slice(0, max - 1) + '…'
}

export function countLines(text: unknown): number {
  if (typeof text !== 'string' || text === '') return 0
  const lines = text.split('\n')
  return text.endsWith('\n') ? lines.length - 1 : lines.length
}

export function relativePath(path: unknown, cwd?: string): string {
  if (typeof path !== 'string') return ''
  if (cwd && isAbsolute(path)) {
    const rel = relative(cwd, path)
    if (rel && !rel.startsWith('..') && !isAbsolute(rel)) return rel
  }
  return path
}

const planStatuses: Record<string, PlanStatus> = {
  pending: 'pending',
  in_progress: 'inProgress',
  inprogress: 'inProgress',
  inProgress: 'inProgress',
  completed: 'completed',
  done: 'completed',
  cancelled: 'canceled',
  canceled: 'canceled',
}

export function planStatus(status: unknown): PlanStatus {
  return (typeof status === 'string' && planStatuses[status]) || 'pending'
}

/** Maps a provider/harness error message to a Flow error code. */
export function classifyError(message: string): CodingErrorCode {
  if (/quota_exceeded|\b402\b|spend limit|payment required/i.test(message)) return 'quotaExceeded'
  if (/model[^\n]{0,80}(not[ _]found|does not exist|unavailable|not supported)|model_not_found|unknown model|invalid model/i.test(message)) {
    return 'modelUnavailable'
  }
  return 'harnessFailed'
}

export function harnessError(message: string): ErrorDetail {
  const clean = message.trim() || 'the agent failed'
  return { code: classifyError(clean), message: truncate(clean, 2000) }
}

export function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined
}

export function asString(value: unknown): string {
  return typeof value === 'string' ? value : value === undefined || value === null ? '' : String(value)
}

export function asNumber(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

/** How a tool call shows up as an action. Shared by Claude Code and OpenCode (lower-cased names). */
export type ToolKind = 'command' | 'edit' | 'read' | 'search' | 'fetch' | 'websearch' | 'task' | 'plan' | 'askUser' | 'flow' | 'other'

const toolKinds: Record<string, ToolKind> = {
  bash: 'command',
  shell: 'command',
  edit: 'edit',
  write: 'edit',
  multiedit: 'edit',
  notebookedit: 'edit',
  patch: 'edit',
  apply_patch: 'edit',
  read: 'read',
  notebookread: 'read',
  ls: 'read',
  list: 'read',
  grep: 'search',
  glob: 'search',
  webfetch: 'fetch',
  websearch: 'websearch',
  task: 'task',
  agent: 'task',
  todowrite: 'plan',
}

/** Names the Flow MCP server's tools get in each harness. */
export function flowToolName(name: string): string | undefined {
  const match = /^(?:mcp__flow__|flow_|flow__)(.+)$/.exec(name)
  return match?.[1]
}

export function toolKind(name: string): ToolKind {
  const flowTool = flowToolName(name)
  if (flowTool === 'ask_user') return 'askUser'
  if (flowTool) return 'flow'
  return toolKinds[name.toLowerCase()] ?? 'other'
}

export function readActionLabel(kind: ToolKind): string {
  return kind === 'search' ? 'Searched' : 'Read'
}

/** The question (plus options) of an ask_user call, as the elicitation body. */
export function askUserBody(input: unknown): string {
  const args = asRecord(input) ?? {}
  const question = asString(args.question).trim() || 'The agent needs your input.'
  const options = Array.isArray(args.options) ? args.options.map(asString).filter(Boolean) : []
  return options.length ? `${question}\n\n${options.map((option) => `- ${option}`).join('\n')}` : question
}

/** Pretty action name for a Flow MCP tool: attach_screenshot → "Attach screenshot". */
export function flowToolLabel(tool: string): string {
  const words = tool.replace(/_/g, ' ').trim()
  return words.charAt(0).toUpperCase() + words.slice(1)
}

/** Counts +/- lines of a unified-diff-like structured patch (Claude's structuredPatch). */
export function countPatchLines(patch: unknown): { additions: number; deletions: number } | undefined {
  if (!Array.isArray(patch)) return undefined
  let additions = 0
  let deletions = 0
  for (const hunk of patch) {
    const lines = asRecord(hunk)?.lines
    if (!Array.isArray(lines)) continue
    for (const line of lines) {
      if (typeof line !== 'string') continue
      if (line.startsWith('+')) additions++
      else if (line.startsWith('-')) deletions++
    }
  }
  return { additions, deletions }
}

/** Counts +/- lines of a unified diff text (OpenCode's metadata.diff). */
export function countUnifiedDiff(diff: unknown): { additions: number; deletions: number } | undefined {
  if (typeof diff !== 'string' || !diff) return undefined
  let additions = 0
  let deletions = 0
  for (const line of diff.split('\n')) {
    if (line.startsWith('+++') || line.startsWith('---')) continue
    if (line.startsWith('+')) additions++
    else if (line.startsWith('-')) deletions++
  }
  return { additions, deletions }
}

/** Parses JSONL text into records, skipping blank and non-JSON lines (harness noise). */
export function parseJsonl(text: string): unknown[] {
  const records: unknown[] = []
  for (const line of text.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed.startsWith('{')) continue
    try {
      records.push(JSON.parse(trimmed))
    } catch {
      // Not JSON: ignore.
    }
  }
  return records
}
