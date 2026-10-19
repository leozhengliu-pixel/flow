import type { CodingDiffSummary, RunnerEvent } from '../protocol.js'
import {
  asNumber,
  asRecord,
  asString,
  askUserBody,
  countLines,
  countPatchLines,
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
  type ToolKind,
  type TurnOutcome,
} from './common.js'

// Claude Code `claude -p --output-format stream-json --verbose` → RunnerEvents.
//
// Records: {type:"system",subtype:"init",session_id,model,...},
// {type:"assistant",message:{content:[thinking|text|tool_use]},parent_tool_use_id},
// {type:"user",message:{content:[tool_result]},tool_use_result},
// {type:"result",subtype:"success"|"error_max_turns"|"error_during_execution",is_error,result,usage,total_cost_usd}.
// Assistant text is held back until the next record: if the turn goes on it
// was a thought; the final text arrives as the result and becomes the response.

interface ToolUse {
  name: string
  kind: ToolKind
  input: Record<string, unknown>
}

export class ClaudeMapper implements StreamMapper {
  private readonly events: EventFactory
  private readonly cwd?: string
  private readonly state: TurnOutcome = { elicited: false, completed: false }
  private readonly tools = new Map<string, ToolUse>()
  private pendingText: string | undefined

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
    const sessionId = asString(rec.session_id)
    if (sessionId) this.state.harnessSessionId = sessionId
    switch (rec.type) {
      case 'system':
        if (rec.subtype === 'init' && rec.model) this.state.model = asString(rec.model)
        return []
      case 'assistant':
        // Subagent (Task) traffic is summarized by the Task action itself.
        if (rec.parent_tool_use_id) return []
        return this.assistant(asRecord(rec.message))
      case 'user':
        if (rec.parent_tool_use_id) return []
        return this.toolResults(asRecord(rec.message), asRecord(rec.tool_use_result))
      case 'result':
        return this.result(rec)
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

  private assistant(message: Record<string, unknown> | undefined): RunnerEvent[] {
    const content = Array.isArray(message?.content) ? message.content : []
    const out: RunnerEvent[] = []
    for (const raw of content) {
      const block = asRecord(raw)
      if (!block) continue
      switch (block.type) {
        case 'thinking': {
          out.push(...this.flushText())
          const thinking = asString(block.thinking).trim()
          if (thinking) out.push(this.events.thought(thinking, true))
          break
        }
        case 'text':
          out.push(...this.flushText())
          this.pendingText = asString(block.text)
          break
        case 'tool_use':
          out.push(...this.flushText())
          out.push(...this.toolUse(asString(block.id), asString(block.name), asRecord(block.input) ?? {}))
          break
      }
    }
    return out
  }

  private toolUse(id: string, name: string, input: Record<string, unknown>): RunnerEvent[] {
    const kind = toolKind(name)
    this.tools.set(id, { name, kind, input })
    switch (kind) {
      case 'command':
        return [this.events.action({ action: 'Ran command', parameter: asString(input.command), tool: name }, true)]
      case 'edit':
        return [this.events.action({ action: 'Editing', parameter: this.editPath(input), tool: name }, true)]
      case 'read':
      case 'search':
        return [this.events.action({ action: readActionLabel(kind), parameter: this.readParameter(input), tool: name }, true)]
      case 'fetch':
        return [this.events.action({ action: 'Fetched', parameter: asString(input.url), tool: name }, true)]
      case 'websearch':
        return [this.events.action({ action: 'Searched the web', parameter: asString(input.query), tool: name }, true)]
      case 'task':
        return [this.events.action({ action: 'Delegated to subagent', parameter: asString(input.description), tool: name }, true)]
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
        return [this.events.action({ action: flowToolLabel(tool), parameter: truncate(JSON.stringify(input)), tool }, true)]
      }
      default:
        return [this.events.action({ action: name, parameter: truncate(JSON.stringify(input)), tool: name }, true)]
    }
  }

  private toolResults(message: Record<string, unknown> | undefined, toolUseResult: Record<string, unknown> | undefined): RunnerEvent[] {
    const content = Array.isArray(message?.content) ? message.content : []
    const out: RunnerEvent[] = []
    for (const raw of content) {
      const block = asRecord(raw)
      if (!block || block.type !== 'tool_result') continue
      const use = this.tools.get(asString(block.tool_use_id))
      if (!use) continue
      const isError = block.is_error === true
      const text = resultText(block.content)
      out.push(...this.toolResult(use, text, isError, toolUseResult))
    }
    return out
  }

  private toolResult(use: ToolUse, text: string, isError: boolean, extra: Record<string, unknown> | undefined): RunnerEvent[] {
    const { name, kind, input } = use
    switch (kind) {
      case 'command': {
        const match = /^(?:Error: )?Exit code (\d+)/m.exec(text)
        const exitCode = match ? Number(match[1]) : isError ? 1 : 0
        return [this.events.action({ action: 'Ran command', parameter: asString(input.command), result: tail(text), tool: name, exitCode })]
      }
      case 'edit': {
        const path = this.editPath(input)
        if (isError) return [this.events.action({ action: 'Edit failed', parameter: path, result: tail(text, 1024), tool: name })]
        return [this.events.action({ action: 'Edited', parameter: path, tool: name }), this.events.diff([this.editDiff(name, input, path, extra)])]
      }
      case 'fetch':
        return [this.events.action({ action: 'Fetched', parameter: asString(input.url), result: isError ? tail(text, 1024) : undefined, tool: name })]
      case 'websearch':
        return [this.events.action({ action: 'Searched the web', parameter: asString(input.query), tool: name })]
      case 'task':
        return [this.events.action({ action: 'Delegated to subagent', parameter: asString(input.description), result: tail(text, 2048), tool: name })]
      case 'flow': {
        const tool = flowToolName(name) ?? name
        return [this.events.action({ action: flowToolLabel(tool), parameter: truncate(JSON.stringify(input)), result: tail(text, 1024), tool })]
      }
      case 'other':
        return [this.events.action({ action: name, parameter: truncate(JSON.stringify(input)), result: tail(text, 1024), tool: name })]
      default:
        // read/search stay ephemeral; plans and questions were mapped on use.
        return []
    }
  }

  private result(rec: Record<string, unknown>): RunnerEvent[] {
    this.state.completed = true
    const out: RunnerEvent[] = []
    const usage = asRecord(rec.usage)
    if (usage) {
      this.state.usage = {
        model: this.state.model ?? '',
        inputTokens: asNumber(usage.input_tokens) + asNumber(usage.cache_creation_input_tokens) + asNumber(usage.cache_read_input_tokens),
        outputTokens: asNumber(usage.output_tokens),
        costUsd: typeof rec.total_cost_usd === 'number' ? rec.total_cost_usd : undefined,
      }
    }
    const resultText = asString(rec.result).trim()
    if (rec.subtype === 'success' && rec.is_error !== true) {
      const finalText = resultText || this.pendingText?.trim() || ''
      this.pendingText = undefined
      this.state.finalText = finalText
      if (finalText) out.push(this.events.response(finalText))
    } else {
      out.push(...this.flushText())
      const errors = Array.isArray(rec.errors) ? rec.errors.map(asString).join('\n') : ''
      const message =
        resultText ||
        errors ||
        (rec.subtype === 'error_max_turns' ? 'The agent reached its maximum number of turns.' : `The agent stopped with ${asString(rec.subtype) || 'an error'}.`)
      this.state.error = harnessError(message)
      out.push(this.events.error(this.state.error))
    }
    if (this.state.usage) out.push(this.events.usage(this.state.usage))
    return out
  }

  private editPath(input: Record<string, unknown>): string {
    return relativePath(input.file_path ?? input.notebook_path ?? input.path, this.cwd)
  }

  private readParameter(input: Record<string, unknown>): string {
    if (input.file_path || input.path) {
      const path = relativePath(input.file_path ?? input.path, this.cwd)
      return input.pattern ? `${asString(input.pattern)} in ${path}` : path
    }
    return asString(input.pattern ?? input.query)
  }

  private editDiff(name: string, input: Record<string, unknown>, path: string, extra: Record<string, unknown> | undefined): CodingDiffSummary {
    const patch = countPatchLines(extra?.structuredPatch)
    const created = extra?.type === 'create'
    if (patch && (patch.additions > 0 || patch.deletions > 0)) {
      return { path, ...patch, status: created ? 'added' : 'modified' }
    }
    switch (name) {
      case 'Write':
        return { path, additions: countLines(input.content), deletions: 0, status: created ? 'added' : 'modified' }
      case 'MultiEdit': {
        let additions = 0
        let deletions = 0
        for (const edit of Array.isArray(input.edits) ? input.edits : []) {
          const e = asRecord(edit) ?? {}
          additions += countLines(e.new_string)
          deletions += countLines(e.old_string)
        }
        return { path, additions, deletions, status: 'modified' }
      }
      case 'NotebookEdit':
        return { path, additions: countLines(input.new_source), deletions: 0, status: 'modified' }
      default:
        return { path, additions: countLines(input.new_string), deletions: countLines(input.old_string), status: 'modified' }
    }
  }
}

function resultText(content: unknown): string {
  if (typeof content === 'string') return content
  if (!Array.isArray(content)) return ''
  return content
    .map((part) => {
      const block = asRecord(part)
      return block?.type === 'text' ? asString(block.text) : ''
    })
    .filter(Boolean)
    .join('\n')
}
