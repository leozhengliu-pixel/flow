import { MCPServerStreamableHttp, type MCPToolFilterCallable } from '@openai/agents'
import type { FlowRunContext } from './context.js'
import { isConnectorTool, ToolErrorFlags } from './run-guard.js'
import { toolOffered } from './tool-groups.js'

/** MCP `_meta` keys of connector tools (api/cmd/server/agent_runtime_connectors.go). */
export const approvalMeta = 'flow/approval'
export const approvedMeta = 'flow/approved'
/** A run-token allowlist entry that admits the run's connector tools. */
export const connectorAllowlist = 'external_*'

/** A Flow connector call may take up to 6 minutes (connectorCallTimeout); Flow bounds every call itself. */
export const flowToolTimeoutMs = 390_000

/** Whether a run-token allowlist admits a tool (connector tools through `external_*` or by name). */
export function allowlisted(allowlist: string[] | undefined, tool: string) {
  if (!allowlist) return true
  return allowlist.includes(tool) || (isConnectorTool(tool) && allowlist.includes(connectorAllowlist))
}

/**
 * Flow's /mcp with a run token: records each connector call's structured `isError` for the run guard (the SDK
 * hands tool guardrails only the result content).
 */
class FlowMcpServer extends MCPServerStreamableHttp {
  constructor(options: ConstructorParameters<typeof MCPServerStreamableHttp>[0], private readonly toolErrors?: ToolErrorFlags) {
    super(options)
  }

  override async callToolResult(...args: Parameters<MCPServerStreamableHttp['callToolResult']>) {
    const result = await super.callToolResult(...args)
    if (isConnectorTool(args[0])) this.toolErrors?.record(args[0], args[1] ?? {}, Boolean(result.isError))
    return result
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
 * - `customDataExtractor` keeps `isError` and Flow's change-set card (`structuredContent.change`) on the tool output
 *   item, for the action activity.
 */
export function flowMcpServer(flowUrl: string, token: string, options: { name?: string; timeoutMs?: number; toolErrors?: ToolErrorFlags } = {}) {
  const toolFilter: MCPToolFilterCallable<FlowRunContext> = async ({ runContext }, tool) => {
    const context = runContext.context
    if (!allowlisted(context.allowlist, tool.name)) return false
    if (isConnectorTool(tool.name) && (tool as { _meta?: Record<string, unknown> })._meta?.[approvalMeta] === 'always') {
      context.alwaysAskTools ??= []
      if (!context.alwaysAskTools.includes(tool.name)) context.alwaysAskTools.push(tool.name)
    }
    return toolOffered(tool.name, context.loadedGroups)
  }
  return new FlowMcpServer({
    url: `${flowUrl.replace(/\/$/, '')}/mcp`,
    name: options.name ?? 'flow',
    cacheToolsList: true,
    timeout: options.timeoutMs ?? flowToolTimeoutMs,
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
  }, options.toolErrors)
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
