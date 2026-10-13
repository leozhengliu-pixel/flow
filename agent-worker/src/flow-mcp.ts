import { MCPServerStreamableHttp, type MCPToolFilterCallable } from '@openai/agents'
import type { FlowRunContext } from './context.js'
import { toolOffered } from './tool-groups.js'

/**
 * Flow's /mcp as the worker's only data tool source, over streamable HTTP with the run token.
 *
 * - The tool list is fetched once per connection (`cacheToolsList`); the callable `toolFilter` runs on every model
 *   turn against the run context, so each turn offers the run-token allowlist ∩ (core tools + loaded groups).
 *   load_tools widens `loadedGroups`, and the next turn sees the new tools.
 * - `customDataExtractor` keeps `isError` and Flow's change-set card (`structuredContent.change`) on the tool output
 *   item, for the action activity.
 */
export function flowMcpServer(flowUrl: string, token: string, options: { name?: string; timeoutMs?: number } = {}) {
  const toolFilter: MCPToolFilterCallable<FlowRunContext> = async ({ runContext }, tool) => {
    const context = runContext.context
    if (context.allowlist && !context.allowlist.includes(tool.name)) return false
    return toolOffered(tool.name, context.loadedGroups)
  }
  return new MCPServerStreamableHttp({
    url: `${flowUrl.replace(/\/$/, '')}/mcp`,
    name: options.name ?? 'flow',
    cacheToolsList: true,
    timeout: options.timeoutMs ?? 60_000,
    clientSessionTimeoutSeconds: 60,
    requestInit: { headers: { Authorization: `Bearer ${token}` } },
    fetch: strictMcpFetch,
    toolFilter: toolFilter as MCPToolFilterCallable,
    errorFunction: ({ error }) => mcpErrorText(error),
    customDataExtractor: ({ isError, structuredContent }) => {
      const change = (structuredContent as Record<string, unknown> | undefined)?.change
      return { isError: Boolean(isError), ...(change ? { change } : {}) }
    },
  })
}

/**
 * The SDK default hides the cause ("An error occurred while running the tool"). Flow answers invalid arguments
 * with a JSON-RPC error (-32602 "Invalid tool arguments") whose reason is only in `data.detail`; the model needs
 * that reason to correct the call, so pass it through.
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
