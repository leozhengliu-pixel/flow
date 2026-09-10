import { Awareness, applyAwarenessUpdate, encodeAwarenessUpdate, removeAwarenessStates } from 'y-protocols/awareness'
import { applyUpdate, encodeStateAsUpdate, encodeStateVector, parseUpdateMeta, type Doc } from 'yjs'
import { realtimeClientId } from '@/lib/api'
import type { User } from '@/types/flow'

const updateFrame = 1
const awarenessFrame = 2

type Status = 'connecting' | 'connected' | 'disconnected' | 'conflict'
type Listener = (event: { status: Status }) => void

interface SyncMessage {
  type: 'document.sync'
  documentId: string
  contentState?: string
  updates: Array<{ id: string; data: string }>
  more?: boolean
}

export class IssueCollaborationProvider {
  readonly awareness: Awareness
  readonly document: Doc
  private readonly workspaceKey: string
  private readonly issueId?: string
  private readonly documentId: string
  private readonly seededWithoutServerState: boolean
  private readonly localUser: { id: string; name: string; color: string }
  private readonly appliedUpdateIds = new Set<string>()
  private readonly pendingUpdates = new Map<string, Uint8Array>()
  private readonly sentUpdates = new Set<string>()
  private readonly serverClocks = new Map<number, number>()
  private pendingBytes = 0
  private deferredUpdate = false
  private receivedServerState = false
  private seedQueued = false
  private readonly listeners = new Set<Listener>()
  private socket?: WebSocket
  private reconnectTimer?: number
  private heartbeatTimer?: number
  private retry = 0
  private destroyed = false
  private started = false
  private synced = false
  private conflicted = false

  constructor({ document, workspaceKey, issueId, documentId, viewer, seededWithoutServerState }: {
    document: Doc
    workspaceKey: string
    issueId?: string
    documentId: string
    viewer: User
    seededWithoutServerState: boolean
  }) {
    this.document = document
    this.workspaceKey = workspaceKey
    this.issueId = issueId
    this.documentId = documentId
    this.seededWithoutServerState = seededWithoutServerState
    this.awareness = new Awareness(document)
    this.localUser = {
      id: viewer.id,
      name: viewer.displayName,
      color: collaborationColor(viewer.id),
    }
    this.awareness.setLocalStateField('user', this.localUser)
    this.document.on('update', this.onDocumentUpdate)
    this.awareness.on('update', this.onAwarenessUpdate)
  }

  start() {
    if (this.destroyed || this.started || this.conflicted) return
    this.started = true
    if (!this.awareness.getLocalState()) this.awareness.setLocalStateField('user', this.localUser)
    this.connect()
  }

  stop() {
    if (!this.started) return
    this.started = false
    this.synced = false
    this.sentUpdates.clear()
    this.receivedServerState = false
    window.clearTimeout(this.reconnectTimer)
    window.clearInterval(this.heartbeatTimer)
    if (this.socket?.readyState === WebSocket.OPEN) {
      removeAwarenessStates(this.awareness, [this.document.clientID], this)
      const update = encodeAwarenessUpdate(this.awareness, [this.document.clientID])
      this.socket.send(encodeFrame(awarenessFrame, this.documentId, '', update))
    }
    this.socket?.close(1000, 'editor suspended')
    this.socket = undefined
    this.emit('disconnected')
  }

  on(event: 'status', listener: Listener) {
    if (event === 'status') this.listeners.add(listener)
  }

  off(event: 'status', listener: Listener) {
    if (event === 'status') this.listeners.delete(listener)
  }

  destroy() {
    if (this.destroyed) return
    this.stop()
    this.destroyed = true
    removeAwarenessStates(this.awareness, [this.document.clientID], this)
    this.document.off('update', this.onDocumentUpdate)
    this.awareness.off('update', this.onAwarenessUpdate)
    this.awareness.destroy()
    this.socket?.close(1000, 'editor closed')
    this.pendingUpdates.clear()
    this.pendingBytes = 0
    this.deferredUpdate = false
    this.serverClocks.clear()
    this.sentUpdates.clear()
    this.appliedUpdateIds.clear()
    this.emit('disconnected')
  }

  updateIds() {
    return [...this.appliedUpdateIds]
  }

  isSynced() {
    return this.synced && !this.conflicted
  }

  acknowledgeSnapshot(updateIds: string[]) {
    updateIds.forEach(id => this.appliedUpdateIds.delete(id))
  }

  private connect = () => {
    if (this.destroyed || !this.started) return
    this.emit('connecting')
    const url = new URL('/api/realtime/socket', window.location.origin)
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
    url.searchParams.set('workspace', this.workspaceKey)
    url.searchParams.set('clientId', realtimeClientId())
    if (import.meta.env.VITE_PAGED_ISSUES === 'true') url.searchParams.set('issues', 'paged')
    const socket = new WebSocket(url)
    socket.binaryType = 'arraybuffer'
    this.socket = socket
    socket.onopen = () => {
      this.retry = 0
      socket.send(JSON.stringify({ type: 'document.join', ...(this.issueId ? { issueId: this.issueId } : {}), documentId: this.documentId }))
    }
    socket.onmessage = event => {
      if (typeof event.data === 'string') this.handleTextMessage(event.data)
      else if (event.data instanceof ArrayBuffer) this.handleBinaryMessage(new Uint8Array(event.data))
    }
    socket.onerror = () => socket.close()
    socket.onclose = () => {
      if (this.socket === socket) this.socket = undefined
      this.synced = false
      this.sentUpdates.clear()
      this.receivedServerState = false
      removeAwarenessStates(this.awareness, [...this.awareness.getStates().keys()].filter(id => id !== this.document.clientID), this)
      this.emit('disconnected')
      if (!this.destroyed && this.started) {
        const delay = Math.min(10_000, 400 * 2 ** this.retry++)
        this.reconnectTimer = window.setTimeout(this.connect, delay)
      }
    }
    window.clearInterval(this.heartbeatTimer)
    this.heartbeatTimer = window.setInterval(() => {
      if (socket.readyState !== WebSocket.OPEN) return
      this.awareness.setLocalStateField('heartbeat', Date.now())
      this.flushPendingUpdates()
    }, 15_000)
  }

  private handleTextMessage(raw: string) {
    let message: SyncMessage | { type?: string }
    try { message = JSON.parse(raw) as SyncMessage }
    catch { return }
    if (message.type === 'document.conflict' && 'documentId' in message && message.documentId === this.documentId) {
      this.conflicted = true
      this.stop()
      this.emit('conflict')
      return
    }
    if (message.type !== 'document.sync' || !('documentId' in message) || message.documentId !== this.documentId) return
    const sync = message as SyncMessage
    this.receivedServerState ||= Boolean(sync.contentState) || sync.updates.length > 0
    if (sync.contentState) this.applyServerUpdate(base64ToBytes(sync.contentState))
    sync.updates.forEach(update => {
      this.applyServerUpdate(base64ToBytes(update.data))
      this.acknowledgeUpdate(update.id)
      this.sentUpdates.delete(update.id)
      this.recordAppliedUpdate(update.id)
    })
    if (sync.more) return
    this.synced = true
    this.flushPendingUpdates()
    this.sendAwarenessUpdate([this.document.clientID])
    this.emit('connected')
  }

  private handleBinaryMessage(raw: Uint8Array) {
    const frame = decodeFrame(raw)
    if (!frame || frame.documentId !== this.documentId) return
    if (frame.kind === updateFrame) {
      this.applyServerUpdate(frame.payload)
      if (frame.updateId) {
        this.acknowledgeUpdate(frame.updateId)
        this.sentUpdates.delete(frame.updateId)
        this.recordAppliedUpdate(frame.updateId)
      }
      this.flushPendingUpdates()
      return
    }
    if (frame.kind === awarenessFrame) applyAwarenessUpdate(this.awareness, frame.payload, this)
  }

  private onDocumentUpdate = (update: Uint8Array, origin: unknown) => {
    if (origin === this) return
    // The initial Markdown seed is read-only until a user edits it. The first
    // local update must include that seed because subsequent structs refer to it.
    const includeSeed = this.seededWithoutServerState && !this.receivedServerState && !this.seedQueued
    this.seedQueued ||= includeSeed
    this.queueDocumentUpdate(includeSeed ? encodeStateAsUpdate(this.document) : update)
  }

  private onAwarenessUpdate = ({ added, updated, removed }: { added: number[]; updated: number[]; removed: number[] }, origin: unknown) => {
    if (origin === this) return
    this.sendAwarenessUpdate([...added, ...updated, ...removed])
  }

  private queueDocumentUpdate(update: Uint8Array) {
    if (this.deferredUpdate || this.pendingUpdates.size >= 128 || this.pendingBytes + update.byteLength > 1024 * 1024) {
      // Y.Doc already owns every edit. Retain a dirty marker while offline or
      // backpressured, then derive a delta against acknowledged server clocks.
      // Do not keep an additional unbounded copy of the document's edit log.
      this.deferredUpdate = true
      this.flushPendingUpdates()
      return
    }
    const updateId = `collab_${crypto.randomUUID()}`
    this.pendingUpdates.set(updateId, update.slice())
    this.pendingBytes += update.byteLength
    this.flushPendingUpdates()
  }

  private acknowledgeUpdate(id: string) {
    this.pendingBytes -= this.pendingUpdates.get(id)?.byteLength ?? 0
    this.pendingUpdates.delete(id)
  }

  private recordAppliedUpdate(id: string) {
    this.appliedUpdateIds.add(id)
    // Read-only observers do not persist snapshots. Bound their bookkeeping
    // without deleting unacknowledged updates from the server's durable log.
    if (this.appliedUpdateIds.size > 10_000) this.appliedUpdateIds.delete(this.appliedUpdateIds.values().next().value!)
  }

  private applyServerUpdate(update: Uint8Array) {
    applyUpdate(this.document, update, this)
    const { from, to } = parseUpdateMeta(update)
    for (const [client, clock] of to) {
      const known = this.serverClocks.get(client) ?? 0
      // Out-of-order gaps stay conservative: resending a Yjs update is safe,
      // advancing past an unacknowledged gap could lose an offline edit.
      if ((from.get(client) ?? 0) <= known) this.serverClocks.set(client, Math.max(known, clock))
    }
  }

  private flushPendingUpdates() {
    if (!this.synced || this.socket?.readyState !== WebSocket.OPEN) return
    if ((this.socket.bufferedAmount ?? 0) > 2 * 1024 * 1024) return
    if (this.deferredUpdate && this.pendingUpdates.size === 0) {
      const update = encodeStateAsUpdate(this.document, encodeStateVector(this.serverClocks))
      this.pendingUpdates.set(`collab_${crypto.randomUUID()}`, update)
      this.pendingBytes = update.byteLength
      this.deferredUpdate = false
    }
    for (const [updateId, update] of this.pendingUpdates) {
      if (this.sentUpdates.has(updateId)) continue
      if ((this.socket.bufferedAmount ?? 0) > 2 * 1024 * 1024) return
      this.socket.send(encodeFrame(updateFrame, this.documentId, updateId, update))
      this.sentUpdates.add(updateId)
    }
  }

  private sendAwarenessUpdate(clients: number[]) {
    if (!clients.length || this.socket?.readyState !== WebSocket.OPEN) return
    this.socket.send(encodeFrame(awarenessFrame, this.documentId, '', encodeAwarenessUpdate(this.awareness, clients)))
  }

  private emit(status: Status) {
    this.listeners.forEach(listener => listener({ status }))
  }
}

function encodeFrame(kind: number, documentId: string, updateId: string, payload: Uint8Array) {
  const document = new TextEncoder().encode(documentId)
  const update = new TextEncoder().encode(updateId)
  const updateHeader = kind === updateFrame ? 2 + update.length : 0
  const frame = new Uint8Array(3 + document.length + updateHeader + payload.length)
  const view = new DataView(frame.buffer)
  frame[0] = kind
  view.setUint16(1, document.length)
  frame.set(document, 3)
  let offset = 3 + document.length
  if (kind === updateFrame) {
    view.setUint16(offset, update.length)
    offset += 2
    frame.set(update, offset)
    offset += update.length
  }
  frame.set(payload, offset)
  return frame
}

function decodeFrame(frame: Uint8Array) {
  if (frame.length < 4) return null
  const view = new DataView(frame.buffer, frame.byteOffset, frame.byteLength)
  const kind = frame[0]
  const documentLength = view.getUint16(1)
  if (!documentLength || frame.length < 3 + documentLength + 1) return null
  const decoder = new TextDecoder()
  const documentId = decoder.decode(frame.slice(3, 3 + documentLength))
  let offset = 3 + documentLength
  let updateId = ''
  if (kind === updateFrame) {
    if (frame.length < offset + 2) return null
    const updateLength = view.getUint16(offset)
    offset += 2
    if (frame.length < offset + updateLength + 1) return null
    updateId = decoder.decode(frame.slice(offset, offset + updateLength))
    offset += updateLength
  }
  if (kind !== updateFrame && kind !== awarenessFrame) return null
  return { kind, documentId, updateId, payload: frame.slice(offset) }
}

function base64ToBytes(value: string) {
  const binary = atob(value)
  const result = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index++) result[index] = binary.charCodeAt(index)
  return result
}

function collaborationColor(value: string) {
  const colors = ['#5e6ad2', '#d15f64', '#2f9e76', '#d78b35', '#8b5fbf', '#3388aa']
  let hash = 0
  for (let index = 0; index < value.length; index++) hash = (hash * 31 + value.charCodeAt(index)) >>> 0
  return colors[hash % colors.length]
}
