import http from 'node:http'

const send = (response, event, data) => response.write(`event: ${event}\ndata: ${JSON.stringify({ type: event, ...data })}\n\n`)

http.createServer(async (request, response) => {
  if (request.method === 'GET' && request.url === '/health') {
    response.writeHead(200).end('ok')
    return
  }
  if (request.method !== 'POST' || request.url !== '/responses') {
    response.writeHead(404).end()
    return
  }
  let raw = ''
  for await (const chunk of request) raw += chunk
  const continued = raw.includes('function_call_output')
  response.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' })
  send(response, 'response.created', { response: { id: continued ? 'resp_2' : 'resp_1' } })
  if (!continued) {
    send(response, 'response.output_item.added', { item: { id: 'item_1', call_id: 'call_1', type: 'function_call', name: 'list_issues', arguments: '' } })
    send(response, 'response.function_call_arguments.done', { item_id: 'item_1', arguments: '{"limit":1}' })
  } else {
    send(response, 'response.reasoning_summary_text.delta', { delta: 'Reviewed workspace context.' })
    await new Promise(resolve => setTimeout(resolve, 30))
    send(response, 'response.output_text.delta', { delta: 'Streaming ' })
    await new Promise(resolve => setTimeout(resolve, 30))
    send(response, 'response.output_text.delta', { delta: 'response' })
  }
  send(response, 'response.completed', { response: { status: 'completed' } })
  response.end()
}).listen(4190, '127.0.0.1')
