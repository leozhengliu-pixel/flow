import { MCPServerStreamableHttp, type MCPToolFilterCallable } from '@openai/agents'
import { AsyncLocalStorage } from 'node:async_hooks'
import { createHash } from 'node:crypto'
import type { FlowRunContext, TurnCall } from './context.js'
import { isConnectorTool, ToolErrorFlags } from './run-guard.js'
import { toolOffered, waitingTools } from './tool-groups.js'

/** MCP `_meta` keys of connector tools (api/cmd/server/agent_runtime_connectors.go). */
export const approvalMeta = 'flow/approval'
export const approvedMeta = 'flow/approved'
/** A run-token allowlist entry that admits the run's connector tools. */
export const connectorAllowlist = 'external_*'

/** Flow's own tools answer well within a minute. */
export const flowToolTimeoutMs = 60_000
/**
 * A connector (external_*) call may take up to 6 minutes on Flow (connectorCallTimeout, which covers a 5-minute
 * question to the person); Flow bounds every call itself.
 */
export const connectorToolTimeoutMs = 390_000

export type FlowMcpOptions = {
  name?: string
  /** Time limit of a Flow tool call (default flowToolTimeoutMs). */
  timeoutMs?: number
  /** Time limit of a connector tool call (default connectorToolTimeoutMs). */
  connectorTimeoutMs?: number
  toolErrors?: ToolErrorFlags
  /** Called when a connector call starts; the returned function is called when it ends. */
  onConnectorCall?: () => () => unknown
  /** Run the calls one at a time, in the order the model made them (a loop run, as in-process). */
  serial?: boolean
  /** The Flow run the token belongs to: each call carries an Idempotency-Key under it (idempotencyKey). */
  runId?: string
}

/** A private `_meta` key that carries a call's Idempotency-Key from the meta resolver to the request; never sent. */
const callKeyMeta = 'flow/idempotency-key'
/** Flow refuses longer keys (api/cmd/server/mcp.go). */
const maxIdempotencyKey = 160

/** The Idempotency-Key of the call in flight in this async context (read by the fetch of its request). */
const callKeys = new AsyncLocalStorage<string>()

function canonicalJson(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(',')}]`
  if (value && typeof value === 'object') {
    return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonicalJson((value as Record<string, unknown>)[key])}`).join(',')}}`
  }
  return JSON.stringify(value) ?? 'null'
}

function parsedArguments(raw: string) {
  try {
    return JSON.parse(raw || '{}')
  } catch {
    return raw
  }
}

/**
 * A call's Idempotency-Key: "<runId>:<callId>:<digest of tool and arguments>". Like the in-process reply
 * (agent_tool_once.go), only the same call (id, tool and arguments) is answered with an earlier run's result: some
 * providers number call ids per response, so an id alone may name a new call. Flow keeps the result per run, so a
 * retried or resumed attempt of the run that sends the same call again gets the first result instead of a second
 * write.
 */
export function idempotencyKey(runId: string, callId: string, tool: string, args: unknown) {
  const digest = createHash('sha256').update(`${tool}\u0000${canonicalJson(args ?? {})}`).digest('hex')
  const key = `${runId}:${callId}:${digest.slice(0, 16)}`
  if (key.length <= maxIdempotencyKey) return key
  return `${runId.slice(0, 80)}:${createHash('sha256').update(`${runId}\u0000${callId}\u0000${digest}`).digest('hex')}`
}

/**
 * The model's call this tool call runs: the latest turn's call of the tool with the same arguments that has not run
 * yet in this process. The SDK hands the meta resolver the tool and arguments, not the call id; the model observer
 * records every turn's calls (context.turns) before the runner runs them.
 */
const claimedCalls = new WeakMap<FlowRunContext, Set<string>>()
function turnCall(context: FlowRunContext, tool: string, args: unknown): TurnCall | undefined {
  const wanted = canonicalJson(args ?? {})
  let claimed = claimedCalls.get(context)
  if (!claimed) claimedCalls.set(context, (claimed = new Set()))
  for (const turn of [...(context.turns ?? [])].reverse()) {
    const call = turn.find(call => call.name === tool && !claimed.has(call.callId) && canonicalJson(parsedArguments(call.arguments)) === wanted)
    if (call) {
      claimed.add(call.callId)
      return call
    }
  }
  return undefined
}

/** Whether a run-token allowlist admits a tool (connector tools through `external_*` or by name). */
export function allowlisted(allowlist: string[] | undefined, tool: string) {
  if (!allowlist) return true
  return allowlist.includes(tool) || (isConnectorTool(tool) && allowlist.includes(connectorAllowlist))
}

type CallArgs = Parameters<MCPServerStreamableHttp['callToolResult']>

/**
 * Flow's /mcp with a run token. The SDK applies one request timeout to every call, so the server is created with
 * the longest limit and each call is cut at its own here: 60 s for Flow's tools, 390 s for connector tools. A connector call's structured `isError`
 * is recorded for the run guard (the SDK hands tool guardrails only the result content), and `onConnectorCall`
 * learns when connector calls are in flight (the worker then watches for the connector's questions).
 */
class FlowMcpServer extends MCPServerStreamableHttp {
  private queue: Promise<unknown> = Promise.resolve()

  constructor(options: ConstructorParameters<typeof MCPServerStreamableHttp>[0], private readonly flow: { timeoutMs: number; connectorTimeoutMs: number; toolErrors?: ToolErrorFlags; onConnectorCall?: () => () => unknown; serial?: boolean }) {
    super(options)
  }

  override async callToolResult(...args: CallArgs) {
    if (!this.flow.serial) return this.callOne(...args)
    const next = this.queue.then(() => this.callOne(...args))
    this.queue = next.catch(() => undefined)
    return next
  }

  private async callOne(...args: CallArgs) {
    const [name, input] = args
    // A chip question waits for the person on Flow (as long as a connector's question).
    if (waitingTools.has(name)) return this.callWithin(this.flow.connectorTimeoutMs, args)
    if (!isConnectorTool(name)) return this.callWithin(this.flow.timeoutMs, args)
    const ended = this.flow.onConnectorCall?.()
    try {
      const result = await this.callWithin(this.flow.connectorTimeoutMs, args)
      this.flow.toolErrors?.record(name, input ?? {}, Boolean(result.isError))
      return result
    } finally {
      await ended?.()
    }
  }

  private async callWithin(timeoutMs: number, [name, input, meta, options]: CallArgs) {
    const timer = new AbortController()
    const handle = setTimeout(() => timer.abort(), timeoutMs)
    const signal = options?.signal ? AbortSignal.any([options.signal, timer.signal]) : timer.signal
    // The call's Idempotency-Key goes out as a header (strictMcpFetch), not in the call's _meta.
    const key = typeof meta?.[callKeyMeta] === 'string' ? meta[callKeyMeta] as string : undefined
    if (meta && callKeyMeta in meta) {
      const { [callKeyMeta]: _key, ...rest } = meta
      meta = Object.keys(rest).length ? rest : undefined
    }
    try {
      if (key) return await callKeys.run(key, () => super.callToolResult(name, input, meta, { ...options, signal }))
      return await super.callToolResult(name, input, meta, { ...options, signal })
    } catch (error) {
      if (timer.signal.aborted && !options?.signal?.aborted) throw new Error(`Request timed out after ${Math.round(timeoutMs / 1000)} s`)
      throw error
    } finally {
      clearTimeout(handle)
    }
  }
}

/**
 * Flow's /mcp as the worker's only data tool source, over streamable HTTP with the run token.
 *
 * - The tool list is fetched once per connection (`cacheToolsList`); the callable `toolFilter` runs on every model
 *   turn against the run context, so each turn offers the run-token allowlist ∩ (core tools + loaded groups).
 *   load_tools widens `loadedGroups`, and the next turn sees the new tools.
 * - Connector tools (external_*) are the run's acting user's MCP connectors, discovered and run by Flow. One whose
 *   connector is set to always ask carries `_meta["flow/approval"] = "always"`: the filter adds it to
 *   `context.alwaysAskTools`, so the call is held for approval (policy.ts), and the approved call is sent with
 *   `_meta["flow/approved"] = true` (Flow refuses it otherwise). Flow records their action activities itself.
 *   A Flow session tool that always waits for the person (file_request) carries the same `_meta` and is held and
 *   sent approved the same way.
 *   A connector may ask the person mid-call (MCP elicitation, answered on Flow): `onConnectorCall` lets the
 *   session watch for those questions while a connector call is in flight (connector-elicitation.ts).
 * - `customDataExtractor` keeps `isError` and Flow's change-set card (`structuredContent.change`) on the tool output
 *   item, for the action activity.
 */
export function flowMcpServer(flowUrl: string, token: string, options: FlowMcpOptions = {}) {
  const toolFilter: MCPToolFilterCallable<FlowRunContext> = async ({ runContext }, tool) => {
    const context = runContext.context
    if (!allowlisted(context.allowlist, tool.name)) return false
    if ((tool as { _meta?: Record<string, unknown> })._meta?.[approvalMeta] === 'always') {
      context.alwaysAskTools ??= []
      if (!context.alwaysAskTools.includes(tool.name)) context.alwaysAskTools.push(tool.name)
    }
    return context.allTools === true || toolOffered(tool.name, context.loadedGroups)
  }
  const limits = { timeoutMs: options.timeoutMs ?? flowToolTimeoutMs, connectorTimeoutMs: options.connectorTimeoutMs ?? connectorToolTimeoutMs }
  return new FlowMcpServer({
    url: `${flowUrl.replace(/\/$/, '')}/mcp`,
    name: options.name ?? 'flow',
    cacheToolsList: true,
    // The SDK's one request limit is the longest; each call is cut at its own limit (FlowMcpServer).
    timeout: Math.max(limits.timeoutMs, limits.connectorTimeoutMs),
    clientSessionTimeoutSeconds: 60,
    requestInit: { headers: { Authorization: `Bearer ${token}` } },
    fetch: strictMcpFetch,
    toolFilter: toolFilter as MCPToolFilterCallable,
    // A held call runs only after the person approved it (needsApproval), so a call of an always-ask tool carries
    // the approval.
    // Each call of a run carries its Idempotency-Key (idempotencyKey), so a retried or resumed attempt does not write
    // twice.
    toolMetaResolver: ({ runContext, toolName, arguments: args }) => {
      const context = runContext.context as FlowRunContext
      const meta: Record<string, unknown> = {}
      if (context.alwaysAskTools?.includes(toolName)) meta[approvedMeta] = true
      const call = options.runId ? turnCall(context, toolName, args) : undefined
      if (options.runId && call) meta[callKeyMeta] = idempotencyKey(options.runId, call.callId, toolName, args)
      return Object.keys(meta).length ? meta : undefined
    },
    errorFunction: ({ error }) => mcpErrorText(error),
    customDataExtractor: ({ isError, structuredContent }) => {
      const change = (structuredContent as Record<string, unknown> | undefined)?.change
      return { isError: Boolean(isError), ...(change ? { change } : {}) }
    },
  }, { ...limits, toolErrors: options.toolErrors, onConnectorCall: options.onConnectorCall, serial: options.serial })
}

/**
 * The SDK default hides the cause ("An error occurred while running the tool"). Flow now answers invalid
 * arguments with an `isError` tool result whose text is the reason, which reaches the model unchanged; protocol
 * errors (and older Flow builds, which put the reason only in JSON-RPC `data.detail`) still come through here, so
 * pass the detail through.
 */
export function mcpErrorText(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error)
  const detail = (error as { data?: { detail?: unknown } })?.data?.detail
  return `Error: ${message}${detail ? ` (${String(detail)})` : ''}`.slice(0, 2000)
}

/**
 * Workaround for a Flow MCP bug: list_cycles / list_issue_statuses return a JSON array as `structuredContent`,
 * which the MCP spec (and the strict MCP client) rejects. Wrap arrays as `{ items }` until mcp.go does.
 */
export const strictMcpFetch: typeof fetch = async (input, init) => {
  const key = callKeys.getStore()
  if (key) {
    const headers = new Headers(init?.headers)
    headers.set('Idempotency-Key', key)
    init = { ...init, headers }
  }
  const response = await fetch(input, init)
  if (!response.headers.get('content-type')?.includes('application/json')) return response
  const text = await response.text()
  let rewritten = text
  try {
    const payload = JSON.parse(text)
    const messages = Array.isArray(payload) ? payload : [payload]
    let changed = false
    for (const message of messages) {
      const result = message?.result
      if (result && Array.isArray(result.structuredContent)) {
        result.structuredContent = { items: result.structuredContent }
        changed = true
      }
    }
    if (changed) rewritten = JSON.stringify(payload)
  } catch {
    // not JSON-RPC; pass through
  }
  const headers = new Headers(response.headers)
  headers.delete('content-length')
  return new Response(rewritten, { status: response.status, statusText: response.statusText, headers })
}
