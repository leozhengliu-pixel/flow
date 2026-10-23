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
  readActionLabel,
  relativePath,
  tail,
  toolKind,
  truncate,
  type MapperOptions,
  type StreamMapper,
  type ToolKind,
  type TurnOutcome,
} from './common.js'

// Pi `pi --mode json` → RunnerEvents.
//
// The first record is the session header {type:"session", id, cwd}; then
// agent_start / turn_start / message_start / message_update (deltas only) /
// message_end {message} / tool_execution_start {toolCallId, toolName, args} /
// tool_execution_end {toolCallId, toolName, result{content, details,
// structuredContent}, isError} / turn_end / agent_end {willRetry} /
// auto_retry_start / auto_retry_end {success, finalError} / compaction_end
// {result.usage} and finally agent_settled {aborted}. An assistant
// message_end carries the authoritative content blocks (thinking, text,
// toolCall), the usage of that model call (input excludes cacheRead and
// cacheWrite; output includes reasoning) and stopReason ("stop", "toolUse",
// "length", "error" with errorMessage, "aborted"). Pi exits 0 even when the
// last model call failed, so the error comes from the stream.

/** Pi's built-in tool names that common.toolKind does not know. */
const piToolKinds: Record<string, ToolKind> = { find: 'search', powershell: 'command' }

export class PiMapper implements StreamMapper {
  private readonly events: EventFactory
  private readonly cwd?: string
  private readonly state: TurnOutcome = { elicited: false, completed: false }
  private readonly toolArgs = new Map<string, Record<string, unknown>>()
  private pendingText: string | undefined
  private lastError: string | undefined
  private settled = false
  private aborted = false
  private inputTokens = 0
  private outputTokens = 0
  private cost = 0
  private sawModelCall = false

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
      case 'session': {
        const id = asString(rec.id)
        if (id) this.state.harnessSessionId = id
        return []
      }
      case 'message_end':
        return this.messageEnd(asRecord(rec.message))
      case 'tool_execution_start': {
        const id = asString(rec.toolCallId)
        const args = asRecord(rec.args)
        if (id && args) this.toolArgs.set(id, args)
        return []
      }
      case 'tool_execution_end':
        return [...this.flushText(), ...this.tool(rec)]
      case 'auto_retry_end':
        if (rec.success === false) this.lastError = asString(rec.finalError) || this.lastError || 'Pi gave up after retrying.'
        return []
      case 'compaction_end':
        this.addUsage(asRecord(asRecord(rec.result)?.usage))
        return []
      case 'agent_settled':
        this.settled = true
        this.aborted = rec.aborted === true
        return []
      default:
        return []
    }
  }

  finish(): RunnerEvent[] {
    const out: RunnerEvent[] = []
    if (this.lastError) {
      this.state.error = harnessError(this.lastError)
      out.push(...this.flushText(), this.events.error(this.state.error))
    } else {
      if (this.pendingText !== undefined) {
        const finalText = this.pendingText.trim()
        this.pendingText = undefined
        if (finalText) {
          this.state.finalText = finalText
          out.push(this.events.response(finalText))
        }
      }
      this.state.completed = this.settled && !this.aborted
    }
    if (this.sawModelCall) {
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

  private messageEnd(message: Record<string, unknown> | undefined): RunnerEvent[] {
    if (!message || message.role !== 'assistant') return []
    this.sawModelCall = true
    const model = asString(message.model)
    if (model) this.state.model = model
    this.addUsage(asRecord(message.usage))
    if (message.stopReason === 'error') {
      this.lastError = asString(message.errorMessage) || 'Pi reported an error.'
      return []
    }
    if (message.stopReason === 'aborted') return []
    // A later successful call (after an automatic retry) clears the error.
    this.lastError = undefined
    const out: RunnerEvent[] = []
    const texts: string[] = []
    const content = Array.isArray(message.content) ? message.content : []
    for (const item of content) {
      const block = asRecord(item) ?? {}
      switch (block.type) {
        case 'thinking': {
          const thinking = asString(block.thinking).trim()
          if (thinking) out.push(...this.flushText(), this.events.thought(thinking, true))
          break
        }
        case 'text': {
          const text = asString(block.text)
          if (text.trim()) texts.push(text)
          break
        }
        case 'toolCall': {
          const id = asString(block.id)
          const args = asRecord(block.arguments)
          if (id && args && !this.toolArgs.has(id)) this.toolArgs.set(id, args)
          break
        }
      }
    }
    if (texts.length) {
      out.push(...this.flushText())
      this.pendingText = texts.join('\n\n')
    }
    return out
  }

  private addUsage(usage: Record<string, unknown> | undefined): void {
    if (!usage) return
    this.inputTokens += asNumber(usage.input) + asNumber(usage.cacheRead) + asNumber(usage.cacheWrite)
    this.outputTokens += asNumber(usage.output)
    this.cost += asNumber(asRecord(usage.cost)?.total)
  }

  private flushText(): RunnerEvent[] {
    if (this.pendingText === undefined) return []
    const text = this.pendingText.trim()
    this.pendingText = undefined
    return text ? [this.events.thought(text)] : []
  }

  private tool(rec: Record<string, unknown>): RunnerEvent[] {
    const name = asString(rec.toolName)
    const id = asString(rec.toolCallId)
    const input = this.toolArgs.get(id) ?? asRecord(rec.args) ?? {}
    this.toolArgs.delete(id)
    const result = asRecord(rec.result) ?? {}
    const details = asRecord(result.details) ?? {}
    const structured = asRecord(result.structuredContent) ?? {}
    const failed = rec.isError === true
    const output = resultText(result)
    const kind = piToolKinds[name] ?? toolKind(name)
    switch (kind) {
      case 'command': {
        const exit = structured.exit_code ?? exitFromText(output)
        const text = typeof structured.output === 'string' ? structured.output : output
        return [
          this.events.action({
            action: 'Ran command',
            parameter: asString(input.command),
            result: tail(text),
            tool: name,
            exitCode: typeof exit === 'number' ? exit : failed ? 1 : 0,
          }),
        ]
      }
      case 'edit': {
        const path = relativePath(input.path ?? input.file_path, this.cwd)
        if (failed) return [this.events.action({ action: 'Edit failed', parameter: path, result: tail(output, 1024), tool: name })]
        return [this.events.action({ action: 'Edited', parameter: path, tool: name }), this.events.diff([this.editDiff(name, input, details, path)])]
      }
      case 'read':
      case 'search': {
        // read/ls take a path; grep/find a pattern (and an optional directory).
        const parameter = kind === 'search' ? asString(input.pattern) : relativePath(input.path, this.cwd)
        return [this.events.action({ action: readActionLabel(kind), parameter: parameter || '.', tool: name }, true)]
      }
      case 'askUser':
        this.state.elicited = true
        return [this.events.elicitation(askUserBody(input))]
      case 'flow': {
        const tool = flowToolName(name) ?? name
        return [this.events.action({ action: flowToolLabel(tool), parameter: truncate(JSON.stringify(input)), result: tail(output, 1024), tool })]
      }
      default:
        if (name === 'tool_search') {
          return [this.events.action({ action: 'Searched tools', parameter: asString(input.query), result: tail(output, 1024), tool: name }, true)]
        }
        // Workspace MCP servers: mcp__<server>__<tool>, shown as server.tool like Codex does.
        const mcp = /^mcp__(.+?)__(.+)$/.exec(name)
        const label = mcp ? `${mcp[1]}.${mcp[2]}` : name
        return [this.events.action({ action: label, parameter: truncate(JSON.stringify(input)), result: tail(output, 1024), tool: mcp ? mcp[2] : name })]
    }
  }

  private editDiff(name: string, input: Record<string, unknown>, details: Record<string, unknown>, path: string): CodingDiffSummary {
    const counted = countUnifiedDiff(details.patch)
    if (counted) return { path, ...counted, status: 'modified' }
    // write: Pi reports no details, so a new file cannot be told from an overwrite.
    if (name === 'write') return { path, additions: countLines(input.content), deletions: 0, status: 'modified' }
    let additions = 0
    let deletions = 0
    for (const item of Array.isArray(input.edits) ? input.edits : []) {
      const edit = asRecord(item) ?? {}
      additions += countLines(edit.newText)
      deletions += countLines(edit.oldText)
    }
    return { path, additions, deletions, status: 'modified' }
  }
}

/** The text blocks of a tool result. */
function resultText(result: Record<string, unknown>): string {
  const content = Array.isArray(result.content) ? result.content : []
  return content
    .map((item) => {
      const block = asRecord(item)
      return block?.type === 'text' ? asString(block.text) : ''
    })
    .filter(Boolean)
    .join('\n')
}

/** Pi's bash tool appends "Command exited with code N" to a failing command's output. */
function exitFromText(text: string): number | undefined {
  const match = /Command exited with code (\d+)\s*$/.exec(text)
  return match ? Number(match[1]) : undefined
}
