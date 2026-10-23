import { createInterface } from 'node:readline'
import type { Readable, Writable } from 'node:stream'
import type { RuntimeClient } from '../runtime-client.js'

// The runner-local MCP server "flow" (stdio, newline-delimited JSON-RPC 2.0).
// Claude Code, Codex, OpenCode and Pi start it as `flow-runner mcp` from the
// configs the runner writes. Tools register through registerTool(); each
// tool lives in its own file under src/mcp/tools/ and is wired in
// src/mcp/tools/index.ts (tracks I and K add their tools there).

export const MCP_PROTOCOL_VERSIONS = ['2025-06-18', '2025-03-26', '2024-11-05']
export const SERVER_INFO = { name: 'flow', version: '0.1.0' }

export interface ToolSchema {
  description: string
  /** JSON Schema of the arguments (type "object"). */
  inputSchema: Record<string, unknown>
}

export type ToolContent = { type: 'text'; text: string } | { type: 'image'; data: string; mimeType: string }

export interface ToolResult {
  content: ToolContent[]
  isError?: boolean
}

export interface ToolContext {
  /** Runtime API client (sandbox token auth); undefined when FLOW_RUNTIME_URL is not set. */
  client?: RuntimeClient
  sandboxId: string
  /** The harness turn this server was started for (FLOW_TURN_ID). */
  turnId?: string
  repoDir: string
  flowDir: string
  browserEnabled: boolean
}

export type ToolHandler = (args: Record<string, unknown>, context: ToolContext) => Promise<ToolResult> | ToolResult

/** The registration interface handed to tool modules (tracks I and K). */
export type RegisterTool = (name: string, schema: ToolSchema, handler: ToolHandler) => void

interface RegisteredTool {
  name: string
  schema: ToolSchema
  handler: ToolHandler
}

interface JsonRpcRequest {
  jsonrpc: '2.0'
  id?: string | number | null
  method: string
  params?: Record<string, unknown>
}

interface JsonRpcResponse {
  jsonrpc: '2.0'
  id: string | number | null
  result?: unknown
  error?: { code: number; message: string; data?: unknown }
}

export const textResult = (text: string, isError = false): ToolResult => ({ content: [{ type: 'text', text }], isError: isError || undefined })

export class McpServer {
  private readonly tools = new Map<string, RegisteredTool>()

  constructor(readonly context: ToolContext) {}

  registerTool(name: string, schema: ToolSchema, handler: ToolHandler): void {
    if (!/^[a-zA-Z0-9_-]{1,64}$/.test(name)) throw new Error(`invalid MCP tool name ${JSON.stringify(name)}`)
    if (this.tools.has(name)) throw new Error(`MCP tool ${name} is already registered`)
    this.tools.set(name, { name, schema, handler })
  }

  toolNames(): string[] {
    return [...this.tools.keys()]
  }

  /** Handles one JSON-RPC message; returns the response, or undefined for notifications. */
  async handle(message: unknown): Promise<JsonRpcResponse | undefined> {
    const request = message as JsonRpcRequest
    const isNotification = request?.id === undefined
    if (!request || typeof request.method !== 'string') {
      return isNotification ? undefined : { jsonrpc: '2.0', id: request?.id ?? null, error: { code: -32600, message: 'Invalid Request' } }
    }
    const id = request.id ?? null
    try {
      const result = await this.dispatch(request.method, request.params ?? {})
      if (isNotification) return undefined
      if (result === METHOD_NOT_FOUND) return { jsonrpc: '2.0', id, error: { code: -32601, message: `Method not found: ${request.method}` } }
      return { jsonrpc: '2.0', id, result }
    } catch (error) {
      if (isNotification) return undefined
      if (error instanceof InvalidParams) return { jsonrpc: '2.0', id, error: { code: -32602, message: error.message } }
      return { jsonrpc: '2.0', id, error: { code: -32603, message: error instanceof Error ? error.message : String(error) } }
    }
  }

  private async dispatch(method: string, params: Record<string, unknown>): Promise<unknown> {
    switch (method) {
      case 'initialize': {
        const requested = typeof params.protocolVersion === 'string' ? params.protocolVersion : ''
        return {
          protocolVersion: MCP_PROTOCOL_VERSIONS.includes(requested) ? requested : MCP_PROTOCOL_VERSIONS[0],
          capabilities: { tools: { listChanged: false } },
          serverInfo: SERVER_INFO,
          instructions: 'Flow sandbox tools. Use ask_user to ask the person who started this session a question.',
        }
      }
      case 'notifications/initialized':
      case 'notifications/cancelled':
        return {}
      case 'ping':
        return {}
      case 'tools/list':
        return {
          tools: [...this.tools.values()].map((tool) => ({
            name: tool.name,
            description: tool.schema.description,
            inputSchema: tool.schema.inputSchema,
          })),
        }
      case 'tools/call': {
        const name = typeof params.name === 'string' ? params.name : ''
        const tool = this.tools.get(name)
        if (!tool) throw new InvalidParams(`Unknown tool: ${name}`)
        const args = params.arguments && typeof params.arguments === 'object' ? (params.arguments as Record<string, unknown>) : {}
        try {
          return await tool.handler(args, this.context)
        } catch (error) {
          return textResult(`${name} failed: ${error instanceof Error ? error.message : String(error)}`, true)
        }
      }
      default:
        return METHOD_NOT_FOUND
    }
  }

  /** Serves newline-delimited JSON-RPC until input closes. */
  serve(input: Readable, output: Writable): Promise<void> {
    const lines = createInterface({ input, crlfDelay: Infinity })
    const pending = new Set<Promise<void>>()
    const write = (response: JsonRpcResponse) => output.write(JSON.stringify(response) + '\n')
    lines.on('line', (line) => {
      const trimmed = line.trim()
      if (!trimmed) return
      let message: unknown
      try {
        message = JSON.parse(trimmed)
      } catch {
        write({ jsonrpc: '2.0', id: null, error: { code: -32700, message: 'Parse error' } })
        return
      }
      const messages = Array.isArray(message) ? message : [message]
      for (const one of messages) {
        const task = this.handle(one).then((response) => {
          if (response) write(response)
        })
        pending.add(task)
        void task.finally(() => pending.delete(task))
      }
    })
    return new Promise((resolve) => {
      lines.once('close', () => {
        void Promise.allSettled([...pending]).then(() => resolve())
      })
    })
  }
}

const METHOD_NOT_FOUND = Symbol('method not found')

class InvalidParams extends Error {}
