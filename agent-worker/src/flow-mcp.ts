import { MCPServerStreamableHttp, type MCPToolFilterCallable } from '@openai/agents'
import type { FlowRunContext } from './context.js'
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
  constructor(options: ConstructorParameters<typeof MCPServerStreamableHttp>[0], private readonly flow: { timeoutMs: number; connectorTimeoutMs: number; toolErrors?: ToolErrorFlags; onConnectorCall?: () => () => unknown }) {
    super(options)
  }

  override async callToolResult(...args: CallArgs) {
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
    try {
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
 *   A connector may ask the person mid-call (MCP elicitation, answered on Flow): `onConnectorCall` lets the
 *   session watch for those questions while a connector call is in flight (connector-elicitation.ts).
 * - `customDataExtractor` keeps `isError` and Flow's change-set card (`structuredContent.change`) on the tool output
 *   item, for the action activity.
 */
export function flowMcpServer(flowUrl: string, token: string, options: FlowMcpOptions = {}) {
  const toolFilter: MCPToolFilterCallable<FlowRunContext> = async ({ runContext }, tool) => {
    const context = runContext.context
    if (!allowlisted(context.allowlist, tool.name)) return false
    if (isConnectorTool(tool.name) && (tool as { _meta?: Record<string, unknown> })._meta?.[approvalMeta] === 'always') {
      context.alwaysAskTools ??= []
      if (!context.alwaysAskTools.includes(tool.name)) context.alwaysAskTools.push(tool.name)
    }
    return toolOffered(tool.name, context.loadedGroups)
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
    toolMetaResolver: ({ runContext, toolName }) =>
      (runContext.context as FlowRunContext).alwaysAskTools?.includes(toolName) ? { [approvedMeta]: true } : undefined,
    errorFunction: ({ error }) => mcpErrorText(error),
    customDataExtractor: ({ isError, structuredContent }) => {
      const change = (structuredContent as Record<string, unknown> | undefined)?.change
      return { isError: Boolean(isError), ...(change ? { change } : {}) }
    },
  }, { ...limits, toolErrors: options.toolErrors, onConnectorCall: options.onConnectorCall })
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
