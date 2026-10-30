/**
 * Small Flow clients for the eval harness: direct MCP calls (fixtures, state verification, cleanup) and the
 * baseline arm (today's in-process agent over /api/agent/sessions/stream). Credentials come from files and are
 * never printed.
 */
export class FlowMcp {
  constructor(private baseUrl: string, private token: string) {}
  async call<T = any>(name: string, args: Record<string, unknown> = {}): Promise<T> {
    const response = await fetch(`${this.baseUrl}/mcp`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${this.token}`, 'content-type': 'application/json', accept: 'application/json' },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name, arguments: args } }),
    })
    const payload = (await response.json()) as any
    if (payload.error) throw new Error(`${name}: ${payload.error.message}`)
    const text = payload.result?.content?.[0]?.text ?? ''
    if (payload.result?.isError) throw new Error(`${name}: ${text.slice(0, 300)}`)
    try { return JSON.parse(text) as T } catch { return text as T }
  }

  async loops<T = any>(): Promise<T> {
    const response = await fetch(`${this.baseUrl}/api/loops`, { headers: { Authorization: `Bearer ${this.token}` } })
    if (!response.ok) throw new Error(`list loops: HTTP ${response.status}`)
    return await response.json() as T
  }
}

export type BaselineEvent = { at: number; type: string; data: any }
export type BaselineTurn = {
  sessionId: string
  status: 'response' | 'error' | 'canceled'
  body: string
  startedAt: number
  firstActivityAt?: number
  firstTextAt?: number
  endedAt: number
  toolCalls: { id?: string; name: string; input: unknown; isError?: boolean }[]
  pendingQuestion?: { partId: string; options: { label: string; value: string }[]; approval: boolean; text: string }
  events: BaselineEvent[]
}

/** Baseline arm: today's in-process agent (POST /api/agent/sessions/stream, SSE). */
export class FlowChatClient {
  constructor(private baseUrl: string, private cookie: string, private workspace: string) {}

  private headers() {
    return { cookie: `flow_clone_session=${this.cookie}`, 'x-workspace-key': this.workspace, 'content-type': 'application/json' }
  }

  async stop(sessionId: string) {
    await fetch(`${this.baseUrl}/api/agent/sessions/${encodeURIComponent(sessionId)}/stop`, { method: 'POST', headers: this.headers() })
  }

  async deleteSession(sessionId: string) {
    await fetch(`${this.baseUrl}/api/agent/sessions/${encodeURIComponent(sessionId)}`, { method: 'DELETE', headers: this.headers() })
  }

  /** Start a session (no sessionId) or continue one; onEvent sees every SSE event as it arrives. */
  async turn(message: string, opts: { sessionId?: string; answer?: { partId: string; value: string }; onEvent?: (e: BaselineEvent, turn: BaselineTurn) => void }): Promise<BaselineTurn> {
    const url = opts.sessionId ? `${this.baseUrl}/api/agent/sessions/${encodeURIComponent(opts.sessionId)}/messages/stream` : `${this.baseUrl}/api/agent/sessions/stream`
    const turn: BaselineTurn = { sessionId: opts.sessionId ?? '', status: 'error', body: '', startedAt: Date.now(), endedAt: 0, toolCalls: [], events: [] }
    const response = await fetch(url, { method: 'POST', headers: this.headers(), body: JSON.stringify(opts.sessionId ? { message, ...(opts.answer ? { answer: opts.answer } : {}) } : { message, location: 'page' }) })
    if (!response.ok || !response.body) {
      turn.body = `HTTP ${response.status}: ${(await response.text()).slice(0, 300)}`
      turn.endedAt = Date.now()
      return turn
    }
    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    let text = ''
    let session: any
    const handle = (block: string) => {
      const data = block.split(/\r?\n/).filter(l => l.startsWith('data:')).map(l => l.slice(5).trimStart()).join('\n')
      if (!data) return
      const event = JSON.parse(data)
      const e: BaselineEvent = { at: Date.now(), type: event.type, data: event }
      turn.events.push(e)
      if (event.type === 'session.started') turn.sessionId ||= event.session?.id ?? ''
      else turn.firstActivityAt ??= e.at
      if (event.type === 'text.delta') { turn.firstTextAt ??= e.at; text += event.delta ?? '' }
      if (event.type === 'text.replaced') text = ''
      const toolName = String(event.part?.toolCall?.name ?? '').replace(/^mcp__flow\./, '')
      if (event.type === 'tool.started' && event.part?.toolCall) turn.toolCalls.push({ id: event.part.id, name: toolName, input: safeJson(event.part.toolCall.arguments) })
      if (event.type === 'tool.completed' && event.part?.toolCall) {
        // Chat Completions streams the tool name after tool.started; take it from the completed part. Approved
        // calls held by an earlier reply complete without a started event.
        let call = turn.toolCalls.find(c => c.id === event.part.id)
        if (!call) turn.toolCalls.push((call = { id: event.part.id, name: toolName, input: safeJson(event.part.toolCall.arguments) }))
        if (!call.name) call.name = toolName
        if (event.part.toolCall.status === 'error') call.isError = true
      }
      if (event.type === 'error') { turn.status = 'error'; turn.body = event.error ?? 'error' }
      if (event.type === 'session.completed') { session = event.session; turn.status = 'response' }
      opts.onEvent?.(e, turn)
    }
    try {
      while (true) {
        const { value, done } = await reader.read()
        buffer += decoder.decode(value, { stream: !done })
        const blocks = buffer.split(/\r?\n\r?\n/)
        buffer = blocks.pop() ?? ''
        for (const block of blocks) handle(block)
        if (done) break
      }
      if (buffer.trim()) handle(buffer)
    } catch (error) {
      turn.body = `stream: ${(error as Error).message}`
    }
    turn.endedAt = Date.now()
    const last = session?.messages?.at(-1)
    if (last?.role === 'assistant') {
      const parts: any[] = last.parts ?? []
      const answer = parts.filter(p => p.type === 'text').map(p => p.text).join('\n')
      if (turn.status === 'response') turn.body = answer || last.content || ''
      const question = parts.find(p => p.type === 'question' && p.question && !p.question.selected && !p.question.answer)
      if (question) turn.pendingQuestion = { partId: question.id, options: question.question.options ?? [], approval: Boolean(question.question.approval), text: question.text ?? '' }
      if (parts.some(p => p.type === 'error')) {
        const err = parts.find(p => p.type === 'error')
        if (/stopp|cancel/i.test(err?.text ?? '')) turn.status = 'canceled'
        else if (turn.status === 'response' && !answer) { turn.status = 'error'; turn.body = err?.text ?? 'error' }
      }
    }
    return turn
  }
}

function safeJson(value: unknown) {
  if (typeof value !== 'string') return value
  try { return JSON.parse(value) } catch { return value }
}
