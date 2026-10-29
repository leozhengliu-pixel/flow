import { webSearchTool, type Tool } from '@openai/agents'
import type { Protocol } from './config.js'

/**
 * The model provider's hosted web search, which a Flow run uses when Flow has no search provider of its own and
 * lets the run search the web (the payload's `hostedWebSearch`, api/cmd/server/agent_runtime_session_tools.go), as
 * the in-process agent adds it: OpenAI Responses' `web_search` tool, or Claude's web search server tool (at most 5
 * searches per turn) through the AI SDK adapter. Chat Completions has none; the run then searches with what Flow's
 * /mcp offers (fetch_url).
 */
export function hostedWebSearchTool(protocol: Protocol): Tool | undefined {
  switch (protocol) {
    case 'openai-responses':
      return webSearchTool() as Tool
    case 'anthropic':
      return { type: 'hosted_tool', name: 'web_search', providerData: { aiSdkProviderTool: { id: 'anthropic.web_search_20250305', args: { maxUses: 5 } } } } as unknown as Tool
    default:
      return undefined
  }
}

/** A provider that refuses the hosted search tool (an OpenAI-compatible gateway without it): the run goes on without. */
export function hostedSearchRefused(error: unknown): boolean {
  const status = (error as { status?: unknown })?.status
  const text = `${(error as Error)?.message ?? error}`
  return (status === 400 || /\b400\b/.test(text)) && /web_search/i.test(text)
}

/** The query of a hosted search call (Responses `web_search_call` action, Anthropic server tool input). */
export function hostedSearchQuery(raw: unknown): string {
  const item = raw as { providerData?: { action?: { query?: string; queries?: string[] } }; arguments?: string; action?: { query?: string } }
  const action = item?.providerData?.action ?? item?.action
  if (action?.query) return action.query
  if (action && 'queries' in action && Array.isArray((action as { queries?: string[] }).queries)) return ((action as { queries: string[] }).queries).join(', ')
  try {
    return String((JSON.parse(item?.arguments ?? '{}') as { query?: string }).query ?? '')
  } catch {
    return ''
  }
}
