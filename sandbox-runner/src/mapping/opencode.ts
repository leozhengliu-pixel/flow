import type { CodingDiffSummary, RunnerEvent } from '../protocol.js'
import {
  asNumber,
  asRecord,
  asString,
  askUserBody,
  countLines,
  countUnifiedDiff,
  EventFactory,
  flowToolLabel,
  flowToolName,
  harnessError,
  planStatus,
  readActionLabel,
  relativePath,
  tail,
  toolKind,
  truncate,
  type MapperOptions,
  type StreamMapper,
  type TurnOutcome,
} from './common.js'

// OpenCode `opencode run --format json` → RunnerEvents.
//
// Every record is {type, timestamp, sessionID, part|error}:
// step_start; text {part.text}; reasoning {part.text} (with --thinking);
// tool_use {part.tool, part.state{status:"completed"|"error",input,output,metadata,error}}
// (emitted once, when the call finishes); step_finish {part.reason, part.cost, part.tokens};
// error {error:{name,data:{message}}}. The run ends when the session goes idle.

export class OpenCodeMapper implements StreamMapper {
  private readonly events: EventFactory
  private readonly cwd?: string
  private readonly state: TurnOutcome = { elicited: false, completed: false }
  private pendingText: string | undefined
  private inputTokens = 0
  private outputTokens = 0
  private cost = 0
  private sawStep = false

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
    const sessionId = asString(rec.sessionID)
    if (sessionId) this.state.harnessSessionId = sessionId
    const part = asRecord(rec.part) ?? {}
    switch (rec.type) {
      case 'text': {
        const out = this.flushText()
        this.pendingText = asString(part.text)
        return out
      }
      case 'reasoning': {
        const text = asString(part.text).trim()
        return text ? [...this.flushText(), this.events.thought(text, true)] : []
      }
      case 'tool_use':
        return [...this.flushText(), ...this.tool(part)]
      case 'step_finish':
        return this.stepFinish(part)
      case 'error':
        return this.error(asRecord(rec.error))
      default:
        return []
    }
  }

  finish(): RunnerEvent[] {
    const out: RunnerEvent[] = []
    if (this.state.error) {
      out.push(...this.flushText())
    } else if (this.pendingText !== undefined) {
      const finalText = this.pendingText.trim()
      this.pendingText = undefined
      if (finalText) {
        this.state.finalText = finalText
        out.push(this.events.response(finalText))
      }
      this.state.completed = this.sawStep
    } else {
      this.state.completed = this.sawStep
    }
    if (this.sawStep) {
      this.state.usage = {
        model: this.state.model ?? '',
        inputTokens: this.inputTokens,
        outputTokens: this.outputTokens,
        costUsd: this.cost || undefined,
      }
      out.push(this.events.usage(this.state.usage))
    }
    return out
  }

  private flushText(): RunnerEvent[] {
    if (this.pendingText === undefined) return []
    const text = this.pendingText.trim()
    this.pendingText = undefined
    return text ? [this.events.thought(text)] : []
  }

  private tool(part: Record<string, unknown>): RunnerEvent[] {
    const name = asString(part.tool)
    const state = asRecord(part.state) ?? {}
    const input = asRecord(state.input) ?? {}
    const metadata = asRecord(state.metadata) ?? {}
    const failed = state.status === 'error'
    const output = failed ? asString(state.error) : asString(state.output)
    const kind = toolKind(name)
    switch (kind) {
      case 'command': {
        const exit = metadata.exit
        return [
          this.events.action({
            action: 'Ran command',
            parameter: asString(input.command),
            result: tail(output),
            tool: name,
            exitCode: typeof exit === 'number' ? exit : failed ? 1 : 0,
          }),
        ]
      }
      case 'edit': {
        const path = relativePath(input.filePath ?? input.file_path ?? input.path, this.cwd)
        if (failed) return [this.events.action({ action: 'Edit failed', parameter: path, result: tail(output, 1024), tool: name })]
        return [this.events.action({ action: 'Edited', parameter: path, tool: name }), this.events.diff([this.editDiff(name, input, metadata, path)])]
      }
      case 'read':
      case 'search': {
        const target = input.filePath ?? input.path ?? input.pattern
        return [this.events.action({ action: readActionLabel(kind), parameter: relativePath(target, this.cwd) || asString(target), tool: name }, true)]
      }
      case 'fetch':
        return [this.events.action({ action: 'Fetched', parameter: asString(input.url), tool: name })]
      case 'websearch':
        return [this.events.action({ action: 'Searched the web', parameter: asString(input.query), tool: name })]
      case 'task':
        return [this.events.action({ action: 'Delegated to subagent', parameter: asString(input.description), result: tail(output, 2048), tool: name })]
      case 'plan': {
        const todos = Array.isArray(input.todos) ? input.todos : []
        return [
          this.events.plan(
            todos.map((todo) => {
              const item = asRecord(todo) ?? {}
              return { content: asString(item.content), status: planStatus(item.status) }
            }),
          ),
        ]
      }
      case 'askUser':
        this.state.elicited = true
        return [this.events.elicitation(askUserBody(input))]
      case 'flow': {
        const tool = flowToolName(name) ?? name
        return [this.events.action({ action: flowToolLabel(tool), parameter: truncate(JSON.stringify(input)), result: tail(output, 1024), tool })]
      }
      default:
        return [this.events.action({ action: name, parameter: truncate(JSON.stringify(input)), result: tail(output, 1024), tool: name })]
    }
  }

  private editDiff(name: string, input: Record<string, unknown>, metadata: Record<string, unknown>, path: string): CodingDiffSummary {
    const counted = countUnifiedDiff(metadata.diff)
    if (counted) return { path, ...counted, status: name === 'write' && metadata.exists === false ? 'added' : 'modified' }
    if (name === 'write') return { path, additions: countLines(input.content), deletions: 0, status: metadata.exists === false ? 'added' : 'modified' }
    return { path, additions: countLines(input.newString), deletions: countLines(input.oldString), status: 'modified' }
  }

  private stepFinish(part: Record<string, unknown>): RunnerEvent[] {
    this.sawStep = true
    const tokens = asRecord(part.tokens) ?? {}
    const cache = asRecord(tokens.cache) ?? {}
    this.inputTokens += asNumber(tokens.input) + asNumber(cache.read) + asNumber(cache.write)
    this.outputTokens += asNumber(tokens.output) + asNumber(tokens.reasoning)
    this.cost += asNumber(part.cost)
    return []
  }

  private error(error: Record<string, unknown> | undefined): RunnerEvent[] {
    const data = asRecord(error?.data)
    const message = asString(data?.message) || asString(error?.name) || 'OpenCode reported an error.'
    this.state.error = harnessError(message)
    return [...this.flushText(), this.events.error(this.state.error)]
  }
}
