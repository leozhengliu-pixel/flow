import http from 'node:http'

const send = (response, event, data) => response.write(`event: ${event}\ndata: ${JSON.stringify({ type: event, ...data })}\n\n`)
const findDocumentUrl = value => {
  if (!value || typeof value !== 'object') return undefined
  if (typeof value.url === 'string' && value.url.includes('/document/')) return value.url
  for (const child of Object.values(value)) {
    const result = findDocumentUrl(child)
    if (result) return result
  }
}

http.createServer(async (request, response) => {
  if (request.url === '/health') return response.writeHead(200).end('ok')
  if (request.method !== 'POST' || request.url !== '/responses') return response.writeHead(404).end()
  let raw = ''
  for await (const chunk of request) raw += chunk
  const payload = JSON.parse(raw)
  const outputs = (payload.input ?? []).filter(item => item.type === 'function_call_output')
  response.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' })
  send(response, 'response.created', { response: { id: 'scale-p13-response' } })
  if (!outputs.length) {
    send(response, 'response.output_item.added', { item: { id: 'scale-p13-document', call_id: 'scale-p13-save', type: 'function_call', name: 'save_document', arguments: '' } })
    send(response, 'response.function_call_arguments.done', { item_id: 'scale-p13-document', arguments: JSON.stringify({ title: 'P13 Agent checklist', content: 'P13 Agent-created document body.' }) })
  } else {
    let url
    for (const output of outputs) {
      try { url = findDocumentUrl(JSON.parse(output.output)) ?? url } catch { /* A failed tool can return plain text. */ }
    }
    send(response, 'response.output_text.delta', { delta: url ? `Created [P13 Agent checklist](${url}).` : 'Created the document.' })
  }
  send(response, 'response.completed', { response: { status: 'completed' } })
  response.end()
}).listen(Number(process.env.FLOW_AGENT_PROVIDER_PORT ?? 4190), '127.0.0.1')
