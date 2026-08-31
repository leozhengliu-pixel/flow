import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Doc } from 'yjs'
import { viewer } from '@/test/fixtures'

vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'client-1' }))

class MockWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  static instances: MockWebSocket[] = []
  readonly url: string
  readyState = MockWebSocket.CONNECTING
  binaryType = ''
  sent: unknown[] = []
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null

  constructor(url: string | URL) {
    this.url = String(url)
    MockWebSocket.instances.push(this)
  }

  send(value: unknown) { this.sent.push(value) }
  open() { this.readyState = MockWebSocket.OPEN; this.onopen?.(new Event('open')) }
  message(data: unknown) { this.onmessage?.({ data } as MessageEvent) }
  close() {
    if (this.readyState === MockWebSocket.CLOSED) return
    this.readyState = MockWebSocket.CLOSED
    this.onclose?.(new CloseEvent('close'))
  }
}

describe('issue collaboration provider', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    MockWebSocket.instances = []
    vi.stubGlobal('WebSocket', MockWebSocket)
    vi.spyOn(crypto, 'randomUUID').mockReturnValue('00000000-0000-4000-8000-000000000002')
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('joins, syncs, sends updates, acknowledges echoes, and reconnects', async () => {
    const { IssueCollaborationProvider } = await import('./issue-collaboration')
    const document = new Doc()
    const provider = new IssueCollaborationProvider({ document, workspaceKey: 'workspace', issueId: 'issue-1', documentId: 'document-1', viewer, seededWithoutServerState: false })
    const statuses: string[] = []
    provider.on('status', event => statuses.push(event.status))
    provider.start()
    expect(MockWebSocket.instances).toHaveLength(1)
    const socket = MockWebSocket.instances[0]
    expect(socket.url).toContain('workspace=workspace')
    socket.open()
    expect(JSON.parse(String(socket.sent[0]))).toEqual({ type: 'document.join', issueId: 'issue-1', documentId: 'document-1' })
    socket.message(JSON.stringify({ type: 'document.sync', documentId: 'document-1', updates: [] }))
    socket.sent = []

    document.getText('content').insert(0, 'hello')
    const updateFrame = socket.sent.find(value => value instanceof Uint8Array && value[0] === 1) as Uint8Array
    expect(updateFrame).toBeInstanceOf(Uint8Array)
    socket.message(updateFrame.buffer)
    expect(provider.updateIds()).toHaveLength(1)
    provider.acknowledgeSnapshot(provider.updateIds())
    expect(provider.updateIds()).toEqual([])

    socket.close()
    expect(statuses).toContain('disconnected')
    vi.advanceTimersByTime(400)
    expect(MockWebSocket.instances).toHaveLength(2)
    provider.stop()
    provider.destroy()
  })

  it('ignores malformed and unrelated sync messages and prevents duplicate starts', async () => {
    const { IssueCollaborationProvider } = await import('./issue-collaboration')
    const provider = new IssueCollaborationProvider({ document: new Doc(), workspaceKey: 'workspace', documentId: 'document-1', viewer, seededWithoutServerState: true })
    provider.start()
    provider.start()
    expect(MockWebSocket.instances).toHaveLength(1)
    const socket = MockWebSocket.instances[0]
    socket.open()
    socket.message('not-json')
    socket.message(JSON.stringify({ type: 'document.sync', documentId: 'other', updates: [] }))
    socket.message(new Uint8Array([1, 0, 0]).buffer)
    expect(provider.updateIds()).toEqual([])
    provider.destroy()
    provider.destroy()
  })
})
