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
  const projectDraft = raw.includes('Draft a new project from the request below.')
  const inviteConversation = raw.includes('Create a team called Invite Test and add 3 people')
  response.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache' })
  send(response, 'response.created', { response: { id: continued ? 'resp_2' : 'resp_1' } })
  if (inviteConversation) {
    // The phone conversation screen: one team write, one documentation search, then the answer.
    const toolCalls = (raw.match(/"type":"function_call"/g) || []).length
    if (toolCalls === 0) {
      send(response, 'response.output_item.added', { item: { id: 'item_invite_save', call_id: 'call_invite_save', type: 'function_call', name: 'save_team', arguments: '' } })
      send(response, 'response.function_call_arguments.done', { item_id: 'item_invite_save', arguments: '{"name":"Invite Test","key":"INV"}' })
    } else if (toolCalls === 1) {
      send(response, 'response.output_item.added', { item: { id: 'item_invite_search', call_id: 'call_invite_search', type: 'function_call', name: 'search_documentation', arguments: '' } })
      send(response, 'response.function_call_arguments.done', { item_id: 'item_invite_search', arguments: '{"query":"invite workspace members"}' })
    } else {
      send(response, 'response.reasoning_summary_text.delta', { delta: 'Created the team and checked the invite flow.' })
      await new Promise(resolve => setTimeout(resolve, 30))
      send(response, 'response.output_text.delta', { delta: 'Created the team, but I can\u2019t send workspace invitations here, so the three people haven\u2019t been added.\n\nTo finish, go to **Settings \u2192 Administration \u2192 Members \u2192 Invite**, paste `qa-invite-1@example.com, qa-invite-2@example.com, qa-invite-3@example.com`, select the new team, and click **Send invites**.' })
    }
  } else if (projectDraft) {
    send(response, 'response.output_text.delta', { delta: 'I drafted a project for you.\n\n```json\n{"name":"Agent launch","summary":"Launch planning","description":"A project drafted by the assistant.","targetDate":"2027-06-30","milestones":["Beta"]}\n```' })
  } else if (!continued) {
    send(response, 'response.output_item.added', { item: { id: 'item_1', call_id: 'call_1', type: 'function_call', name: 'list_issues', arguments: '' } })
    send(response, 'response.function_call_arguments.done', { item_id: 'item_1', arguments: '{"limit":1}' })
  } else {
    send(response, 'response.reasoning_summary_text.delta', { delta: 'Reviewed workspace context.' })
    await new Promise(resolve => setTimeout(resolve, 30))
    send(response, 'response.output_text.delta', { delta: '## Result\n\n1. **Streaming** ' })
    await new Promise(resolve => setTimeout(resolve, 30))
    send(response, 'response.output_text.delta', { delta: 'response\n2. `Verified`' })
  }
  send(response, 'response.completed', { response: { status: 'completed' } })
  response.end()
}).listen(4190, '127.0.0.1')
