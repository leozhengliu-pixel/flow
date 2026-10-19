import type { CodingDiffSummary, RunnerEvent } from '../protocol.js'
import {
  asNumber,
  asRecord,
  asString,
  askUserBody,
  EventFactory,
  flowToolLabel,
  harnessError,
  relativePath,
  tail,
  truncate,
  type MapperOptions,
  type StreamMapper,
  type TurnOutcome,
} from './common.js'

// Codex `codex exec --json` → RunnerEvents.
//
// Records: {type:"thread.started",thread_id}, {type:"turn.started"},
// {type:"item.started"|"item.updated"|"item.completed",item:{id,type,...}}
// with item types agent_message, reasoning, command_execution, file_change,
// mcp_tool_call, web_search, todo_list and error;
// {type:"turn.completed",usage}, {type:"turn.failed",error:{message}},
// {type:"error",message}.

const RETRY_NOTICE = /retrying|reconnecting|re-connecting/i

export class CodexMapper implements StreamMapper {
  private readonly events: EventFactory
  private readonly cwd?: string
  private readonly state: TurnOutcome = { elicited: false, completed: false }
  private readonly startedItems = new Set<string>()
  private readonly elicitedItems = new Set<string>()
  private pendingText: string | undefined
  private lastErrorMessage = ''

  constructor(options: MapperOptions = {}) {
    this.events = new EventFactory(options)
    this.cwd = options.cwd
    this.state.model = options.model || undefined
  }

  outcome(): TurnOutcome {
    return { ...this.state }
  }

  map(record: unknown): RunnerEvent[] {
    const rec = asRecord(record)
    if (!rec) return []
    switch (rec.type) {
      case 'thread.started':
        this.state.harnessSessionId = asString(rec.thread_id) || this.state.harnessSessionId
        return []
      case 'item.started':
      case 'item.updated':
      case 'item.completed':
        return this.item(rec.type, asRecord(rec.item))
      case 'turn.completed':
        return this.turnCompleted(asRecord(rec.usage))
      case 'turn.failed':
        return this.failed(asString(asRecord(rec.error)?.message) || 'The turn failed.')
      case 'error': {
        const message = asString(rec.message)
        if (RETRY_NOTICE.test(message)) return [this.events.thought(truncate(message, 500), true)]
        return this.failed(message || 'Codex reported an error.')
      }
      default:
        return []
    }
  }

  finish(): RunnerEvent[] {
    return this.flushText()
  }

  private flushText(): RunnerEvent[] {
    if (this.pendingText === undefined) return []
    const text = this.pendingText.trim()
    this.pendingText = undefined
    return text ? [this.events.thought(text)] : []
  }

  private item(phase: string, item: Record<string, unknown> | undefined): RunnerEvent[] {
    if (!item) return []
    const id = asString(item.id)
    const started = phase === 'item.started'
    const completed = phase === 'item.completed'
    if (started) this.startedItems.add(id)
    switch (item.type) {
      case 'reasoning': {
        const text = asString(item.text).trim()
        return completed && text ? [this.events.thought(text, true)] : []
      }
      case 'agent_message': {
        if (!completed) return []
        const out = this.flushText()
        this.pendingText = asString(item.text)
        return out
      }
      case 'command_execution':
        return this.command(item, started, completed)
      case 'file_change':
        return completed ? [...this.flushText(), ...this.fileChange(item)] : []
      case 'mcp_tool_call':
        return this.mcpToolCall(id, item, started, completed)
      case 'web_search': {
        const query = asString(item.query)
        if (started) return [...this.flushText(), this.events.action({ action: 'Searched the web', parameter: query, tool: 'web_search' }, true)]
        return completed ? [this.events.action({ action: 'Searched the web', parameter: query, tool: 'web_search' })] : []
      }
      case 'todo_list':
        return [this.events.plan(todoPlan(item.items))]
      case 'error': {
        const message = asString(item.message)
        return completed && message ? [this.events.thought(truncate(message, 1000), true)] : []
      }
      default:
        return []
    }
  }

  private command(item: Record<string, unknown>, started: boolean, completed: boolean): RunnerEvent[] {
    const command = unwrapShell(asString(item.command))
    if (started) return [...this.flushText(), this.events.action({ action: 'Ran command', parameter: command, tool: 'shell' }, true)]
    if (!completed) return []
    const exit = item.exit_code
    return [
      ...this.flushText(),
      this.events.action({
        action: 'Ran command',
        parameter: command,
        result: tail(asString(item.aggregated_output)),
        tool: 'shell',
        exitCode: typeof exit === 'number' ? exit : item.status === 'failed' ? 1 : undefined,
      }),
    ]
  }

  private fileChange(item: Record<string, unknown>): RunnerEvent[] {
    const changes = Array.isArray(item.changes) ? item.changes : []
    const diff: CodingDiffSummary[] = changes.map((raw) => {
      const change = asRecord(raw) ?? {}
      const kind = asString(change.kind)
      return {
        path: relativePath(change.path, this.cwd),
        additions: 0,
        deletions: 0,
        status: kind === 'add' ? 'added' : kind === 'delete' ? 'deleted' : 'modified',
      }
    })
    const paths = diff.map((change) => change.path).join(', ')
    if (item.status === 'failed') return [this.events.action({ action: 'Edit failed', parameter: paths, tool: 'apply_patch' })]
    const out = [this.events.action({ action: 'Edited', parameter: paths, tool: 'apply_patch' })]
    if (diff.length) out.push(this.events.diff(diff))
    return out
  }

  private mcpToolCall(id: string, item: Record<string, unknown>, started: boolean, completed: boolean): RunnerEvent[] {
    const server = asString(item.server)
    const tool = asString(item.tool)
    const args = item.arguments
    if (server === 'flow' && tool === 'ask_user') {
      if (this.elicitedItems.has(id)) return []
      this.elicitedItems.add(id)
      this.state.elicited = true
      return [...this.flushText(), this.events.elicitation(askUserBody(args))]
    }
    const label = server === 'flow' ? flowToolLabel(tool) : `${server}.${tool}`
    const parameter = truncate(typeof args === 'string' ? args : JSON.stringify(args ?? {}))
    if (started) return [...this.flushText(), this.events.action({ action: label, parameter, tool }, true)]
    if (!completed) return []
    const error = asString(asRecord(item.error)?.message ?? item.error)
    const result = error || mcpResultText(item.result)
    return [this.events.action({ action: label, parameter, result: tail(result, 1024), tool })]
  }

  private turnCompleted(usage: Record<string, unknown> | undefined): RunnerEvent[] {
    this.state.completed = true
    const out: RunnerEvent[] = []
    const finalText = this.pendingText?.trim() ?? ''
    this.pendingText = undefined
    if (finalText) {
      this.state.finalText = finalText
      out.push(this.events.response(finalText))
    }
    if (usage) {
      this.state.usage = {
        model: this.state.model ?? '',
        inputTokens: asNumber(usage.input_tokens),
        outputTokens: asNumber(usage.output_tokens),
      }
      out.push(this.events.usage(this.state.usage))
    }
    return out
  }

  private failed(message: string): RunnerEvent[] {
    const out = this.flushText()
    if (message === this.lastErrorMessage) return out
    this.lastErrorMessage = message
    this.state.error = harnessError(message)
    out.push(this.events.error(this.state.error))
    return out
  }
}

/** Codex reports commands as `bash -lc '<cmd>'`; show the inner command. */
export function unwrapShell(command: string): string {
  const match = /^(?:\/(?:usr\/)?bin\/)?(?:ba|z)?sh -l?c (['"])([\s\S]*)\1$/.exec(command.trim())
  if (!match) return command
  return match[1] === "'" ? match[2].replace(/'\\''/g, "'") : match[2].replace(/\\"/g, '"')
}

function todoPlan(items: unknown) {
  const list = Array.isArray(items) ? items.map((raw) => asRecord(raw) ?? {}) : []
  const firstOpen = list.findIndex((item) => item.completed !== true)
  return list.map((item, index) => ({
    content: asString(item.text),
    status: item.completed === true ? ('completed' as const) : index === firstOpen ? ('inProgress' as const) : ('pending' as const),
  }))
}

function mcpResultText(result: unknown): string {
  const rec = asRecord(result)
  const content = Array.isArray(rec?.content) ? rec.content : []
  const text = content
    .map((part) => {
      const block = asRecord(part)
      return block?.type === 'text' ? asString(block.text) : ''
    })
    .filter(Boolean)
    .join('\n')
  return text || (result === undefined || result === null ? '' : JSON.stringify(result))
}
