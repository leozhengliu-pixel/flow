import { expect } from '@playwright/test'
import * as Y from 'yjs'
import { test as workspaceTest } from '../workspace-fixture'
import { startApi, type RunningApi } from '../security/harness'

const test = workspaceTest.extend<{ api: RunningApi; presenceMode: string }>({
  presenceMode: ['on', { option: true }],
  api: async ({ presenceMode }, provide) => {
    const api = await startApi({ csp: 'off', env: { FLOW_AUTH_DISABLED: 'true', FLOW_PRESENCE_MINIMAL: presenceMode } })
    try { await provide(api) } finally { await api.stop() }
  },
  baseURL: async ({ api }, provide) => { await provide(api.origin) },
})

function largestLegalUpdate(documentID: string) {
  const target = (2 << 20) - 5 - new TextEncoder().encode(documentID).length - 'collab_p06_browser_largest'.length
  let textLength = target
  for (let attempt = 0; attempt < 4; attempt++) {
    const doc = new Y.Doc()
    doc.clientID = 7
    const paragraph = new Y.XmlElement('paragraph')
    const text = new Y.XmlText()
    text.insert(0, 'x'.repeat(textLength))
    paragraph.insert(0, [text])
    doc.getXmlFragment('prosemirror').insert(0, [paragraph])
    const update = Y.encodeStateAsUpdate(doc)
    doc.destroy()
    if (update.length === target) return update
    textLength += target - update.length
  }
  throw new Error('could not construct the maximum legal Yjs update')
}

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test.describe(`${viewport.width}px`, () => {
    test.use({ viewport, workspaceNamePrefix: 'P06' })

    test('minimal heartbeat and old roster coexist', async ({ api, page, workspaceKey }) => {
      await page.goto(`${api.origin}/sw.js`)
      const result = await page.evaluate(async workspace => {
        const heartbeat = async (minimal: boolean) => {
          const response = await fetch('/api/realtime/presence', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'X-Workspace-Key': workspace, ...(minimal ? { 'X-Flow-Presence': 'minimal' } : {}) },
            body: JSON.stringify({ clientId: 'p06-browser', route: `/${workspace}/inbox` }),
          })
          return { status: response.status, text: await response.text() }
        }
        return { minimal: await heartbeat(true), legacy: await heartbeat(false) }
      }, workspaceKey)
      expect(result.minimal).toEqual({ status: 204, text: '' })
      expect(result.legacy.status).toBe(200)
      expect(JSON.parse(result.legacy.text)).toEqual(expect.arrayContaining([expect.objectContaining({ clientId: 'p06-browser' })]))
    })

    test('largest legal collaboration frame survives disconnect and rejoin', async ({ api, page, request, workspaceKey }) => {
      const created = await request.post('/api/documents', {
        headers: { 'X-Workspace-Key': workspaceKey }, data: { title: 'P06 recovery', content: '' },
      })
      expect(created.status()).toBe(201)
      const document = await created.json() as { id: string; collaborationId?: string }
      const documentID = document.collaborationId || document.id
      const update = largestLegalUpdate(documentID)
      const restored = new Y.Doc()
      Y.applyUpdate(restored, update)
      expect(restored.getXmlFragment('prosemirror').length).toBe(1)
      expect(restored.getXmlFragment('prosemirror').toString().length).toBeGreaterThan(update.length - 256)
      restored.destroy()
      await page.goto(`${api.origin}/sw.js`)
      const result = await page.evaluate(async ({ workspace, documentID, payloadBase64 }) => {
        const encoder = new TextEncoder()
        const document = encoder.encode(documentID)
        const updateID = 'collab_p06_browser_largest'
        const update = encoder.encode(updateID)
        const frame = new Uint8Array(2 << 20)
        frame[0] = 1
        const header = new DataView(frame.buffer)
        header.setUint16(1, document.length)
        frame.set(document, 3)
        const offset = 3 + document.length
        header.setUint16(offset, update.length)
        frame.set(update, offset + 2)
        const payloadBytes = frame.length - offset - 2 - update.length
        const payload = atob(payloadBase64)
        frame.set(Uint8Array.from(payload, character => character.charCodeAt(0)), offset + 2 + update.length)
        const connect = async (client: string) => {
          const socket = new WebSocket(`${location.origin.replace('http', 'ws')}/api/realtime/socket?workspace=${workspace}&clientId=${client}`)
          socket.binaryType = 'arraybuffer'
          await new Promise<void>((resolve, reject) => { socket.onopen = () => resolve(); socket.onerror = () => reject(new Error('socket open failed')) })
          return socket
        }
        const receive = (socket: WebSocket) => new Promise<MessageEvent>((resolve, reject) => {
          const timer = setTimeout(() => reject(new Error('socket receive timed out')), 10_000)
          socket.onmessage = event => { clearTimeout(timer); resolve(event) }
          socket.onclose = () => { clearTimeout(timer); reject(new Error('socket closed before recovery')) }
        })
        const first = await connect('p06-first')
        let pending = receive(first)
        first.send(JSON.stringify({ type: 'document.join', documentId: documentID }))
        await pending
        pending = receive(first)
        first.send(frame)
        const committed = await pending
        first.close()
        const second = await connect('p06-reconnected')
        pending = receive(second)
        second.send(JSON.stringify({ type: 'document.join', documentId: documentID }))
        const recovered = JSON.parse((await pending).data as string) as { updates: Array<{ id: string; data: string }> }
        second.close()
        const entry = recovered.updates.find(update => update.id === updateID)
        const decoded = entry ? atob(entry.data) : ''
        return { committedBytes: (committed.data as ArrayBuffer).byteLength, payloadBytes, recoveredBytes: decoded.length, complete: entry?.data === payloadBase64 }
      }, { workspace: workspaceKey, documentID, payloadBase64: Buffer.from(update).toString('base64') })
      expect(result.committedBytes).toBe(2 << 20)
      expect(result.recoveredBytes).toBe(result.payloadBytes)
      expect(result.complete).toBe(true)
    })
  })
}

test.describe('rollout fallback', () => {
  test.use({ presenceMode: 'off', workspaceNamePrefix: 'P06 fallback' })
  test('new caller can consume the old 200 response when minimal is disabled', async ({ request, workspaceKey }) => {
    const response = await request.post('/api/realtime/presence', {
      headers: { 'X-Workspace-Key': workspaceKey, 'X-Flow-Presence': 'minimal' }, data: { clientId: 'p06-fallback' },
    })
    expect(response.status()).toBe(200)
    expect(await response.json()).toEqual(expect.arrayContaining([expect.objectContaining({ clientId: 'p06-fallback' })]))
  })
})
