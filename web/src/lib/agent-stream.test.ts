import { beforeEach, describe, expect, it, vi } from 'vitest'
import { streamNewAgentSession } from './agent-stream'

function streamResponse(chunks: string[], status = 200) {
  const encoder = new TextEncoder()
  return new Response(new ReadableStream({
    start(controller) {
      chunks.forEach(chunk => controller.enqueue(encoder.encode(chunk)))
      controller.close()
    },
  }), { status, headers: { 'Content-Type': status === 200 ? 'text/event-stream' : 'application/json' } })
}

describe('Agent stream client', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/workspace/agent')
  })

  it('parses split SSE frames and returns the completed session', async () => {
    const session = { id: 'session-1', slugId: 'chat', userId: 'user', title: 'Chat', favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [], createdAt: '', updatedAt: '' }
    const fetchMock = vi.fn().mockResolvedValue(streamResponse([
      'event: session.started\ndata: {"type":"session.started","messageId":"message-1","session":{"id":"session-1","slugId":"chat","userId":"user","title":"Chat","favorite":false,"location":"page","issueIds":[],"skillIds":[],"messages":[],"createdAt":"","updatedAt":""}}\n\n',
      'event: text.delta\ndata: {"type":"text.delta","messageId":"message-1","delta":"Hel',
      'lo"}\n\nevent: session.completed\ndata: {"type":"session.completed","session":' + JSON.stringify(session) + '}\n\n',
    ]))
    vi.stubGlobal('fetch', fetchMock)
    const events: string[] = []
    await expect(streamNewAgentSession({ message: 'Hello', location: 'page' }, event => events.push(event.type))).resolves.toEqual(session)
    expect(events).toEqual(['session.started', 'text.delta', 'session.completed'])
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(new Headers(init.headers).get('X-Workspace-Key')).toBe('workspace')
    expect(init.method).toBe('POST')
  })

  it('surfaces HTTP and stream errors', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ error: 'Unavailable', code: 'provider_error' }), { status: 502, headers: { 'Content-Type': 'application/json' } })))
    await expect(streamNewAgentSession({ message: 'Hello' }, vi.fn())).rejects.toMatchObject({ status: 502, code: 'provider_error', message: 'Unavailable' })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(streamResponse(['event: error\ndata: {"type":"error","error":"Stream failed"}\n\n'])))
    await expect(streamNewAgentSession({ message: 'Hello' }, vi.fn())).rejects.toThrow('Stream failed')
  })
})
