import { describe, expect, it } from 'vitest'
import { FlowClient, FlowConfigurationError, NotFoundFlowError } from '../src/index.js'
import { API_URL, envelope, json, mockFetch } from './helpers.js'

const issue = { id: 'issue_1', identifier: 'ENG-1', title: 'Hello' }

function client(responder: Parameters<typeof mockFetch>[0], options: Partial<ConstructorParameters<typeof FlowClient>[0]> = {}) {
  const mock = mockFetch(responder)
  return { ...mock, client: new FlowClient({ apiKey: 'flow_api_test', apiUrl: API_URL, fetch: mock.fetch, ...options } as ConstructorParameters<typeof FlowClient>[0]) }
}

describe('construction', () => {
  it('requires credentials and an API URL', () => {
    expect(() => new FlowClient({} as never, { apiUrl: API_URL })).toThrow(FlowConfigurationError)
    expect(() => new FlowClient({ apiKey: 'a', accessToken: 'b' } as never, { apiUrl: API_URL })).toThrow(/not both/)
    const previous = { base: process.env.FLOW_BASE_URL, api: process.env.FLOW_API_URL }
    delete process.env.FLOW_BASE_URL
    delete process.env.FLOW_API_URL
    try {
      expect(() => new FlowClient({ apiKey: 'a' })).toThrow(/apiUrl/)
    } finally {
      if (previous.base !== undefined) process.env.FLOW_BASE_URL = previous.base
      if (previous.api !== undefined) process.env.FLOW_API_URL = previous.api
    }
  })

  it('accepts options as a second argument and normalises the API URL', async () => {
    const mock = mockFetch(() => json({ user: { id: 'u' }, actor: 'user', authentication: 'apiKey' }))
    const flow = new FlowClient({ apiKey: 'flow_api_x' }, { apiUrl: 'https://flow.example.com/api/', fetch: mock.fetch })
    expect(flow.apiUrl).toBe('https://flow.example.com')
    await flow.viewer
    expect(mock.calls[0].url).toBe('https://flow.example.com/api/viewer')
  })

  it('keeps a path prefix of the deployment', () => {
    const { client: flow } = client(() => json({}), { apiUrl: 'https://example.com/flow/' })
    expect(flow.apiUrl).toBe('https://example.com/flow')
  })

  it('reads FLOW_BASE_URL', () => {
    const previous = process.env.FLOW_BASE_URL
    process.env.FLOW_BASE_URL = 'https://env.example.com'
    try {
      expect(new FlowClient({ apiKey: 'a' }, { fetch: mockFetch(() => json({})).fetch }).apiUrl).toBe('https://env.example.com')
    } finally {
      if (previous === undefined) delete process.env.FLOW_BASE_URL
      else process.env.FLOW_BASE_URL = previous
    }
  })
})

describe('authentication and headers', () => {
  it('sends the API key as a bearer token', async () => {
    const { client: flow, calls } = client(() => json({ user: { id: 'u' }, actor: 'user', authentication: 'apiKey' }))
    expect((await flow.viewer).user.id).toBe('u')
    expect(calls[0].headers.get('authorization')).toBe('Bearer flow_api_test')
    expect(calls[0].headers.get('accept')).toBe('application/json')
  })

  it('resolves an access token function for every request', async () => {
    let n = 0
    const { client: flow, calls } = client(() => json({ user: { id: 'u' } }), { apiKey: undefined, accessToken: () => `flow_oauth_${++n}` } as never)
    await flow.viewer
    await flow.viewer
    expect(calls.map(call => call.headers.get('authorization'))).toEqual(['Bearer flow_oauth_1', 'Bearer flow_oauth_2'])
  })

  it('merges client headers, request headers and the signed-URL header', async () => {
    const { client: flow, calls } = client(() => json(issue), { headers: { 'X-Trace': 'a', 'X-Other': 'b' }, signedFileUrlsExpireIn: 600 })
    await flow.issue('ENG-1', { headers: { 'X-Trace': 'override' } })
    expect(calls[0].headers.get('x-trace')).toBe('override')
    expect(calls[0].headers.get('x-other')).toBe('b')
    expect(calls[0].headers.get('x-flow-file-urls-expire-in')).toBe('600')
  })
})

describe('reading', () => {
  it('fetches an issue by identifier', async () => {
    const { client: flow, calls } = client(() => json(issue))
    expect((await flow.issue('ENG-1')).identifier).toBe('ENG-1')
    expect(calls[0].path).toBe('/api/issue-records/ENG-1')
  })

  it('encodes path parameters', async () => {
    const { client: flow, calls } = client(() => json(issue))
    await flow.issue('a/b c')
    expect(calls[0].url).toBe(`${API_URL}/api/issue-records/a%2Fb%20c`)
  })

  it('maps issue arguments to the issue connection API', async () => {
    const { client: flow, calls } = client(() => json({ nodes: [issue], pageInfo: { hasNextPage: false, hasPreviousPage: false }, totalCount: 1 }))
    const issues = await flow.issues({
      first: 500,
      filter: { and: [{ field: 'priority', operator: 'is', values: ['urgent'] }] },
      orderBy: 'updatedAt',
      orderDirection: 'asc',
      includeArchived: true,
      includeTotal: true,
      teamId: ['ENG', 'OPS'],
      query: 'crash',
    })
    expect(calls[0].path).toBe('/api/issue-records')
    const query = calls[0].query
    expect(query.get('connection')).toBe('1')
    expect(query.get('first')).toBe('100')
    expect(query.get('last')).toBeNull()
    expect(query.get('orderBy')).toBe('updatedAt')
    expect(query.get('orderDirection')).toBe('asc')
    expect(query.get('includeArchived')).toBe('true')
    expect(query.get('includeTotal')).toBe('true')
    expect(query.get('teamId')).toBe('ENG,OPS')
    expect(query.get('q')).toBe('crash')
    expect(JSON.parse(query.get('filter')!)).toEqual({ and: [{ field: 'priority', operator: 'is', values: ['urgent'] }] })
    expect(issues.nodes).toHaveLength(1)
    expect(issues.totalCount).toBe(1)
  })

  it('orders issues newest first by default and accepts a filter as JSON text', async () => {
    const { client: flow, calls } = client(() => json({ nodes: [], pageInfo: { hasNextPage: false, hasPreviousPage: false } }))
    await flow.issues({ filter: '{"field":"status","values":["started"]}' })
    expect(calls[0].query.get('orderBy')).toBe('createdAt')
    expect(calls[0].query.get('first')).toBe('50')
    expect(calls[0].query.get('filter')).toBe('{"field":"status","values":["started"]}')
  })

  it('pages issues backward with last and before', async () => {
    const { client: flow, calls } = client(() => json({ nodes: [], pageInfo: { hasNextPage: true, hasPreviousPage: false } }))
    await flow.issues({ last: 20, before: 'cursor_x' })
    expect(calls[0].query.get('last')).toBe('20')
    expect(calls[0].query.get('before')).toBe('cursor_x')
    expect(calls[0].query.get('first')).toBeNull()
    await flow.issues({ last: 5 })
    expect(calls[1].query.get('last')).toBe('5')
    expect(calls[1].query.get('before')).toBeNull()
  })

  it('exposes the original issue page as a legacy operation', async () => {
    const { client: flow, calls } = client(() => json({ items: [issue], hasMore: false, total: -1 }))
    const page = await flow.api.getIssueRecordsLegacy({ query: { limit: 5, sort: 'updatedAt' } })
    expect(page.items).toHaveLength(1)
    expect(calls[0].query.get('connection')).toBeNull()
    expect(calls[0].query.get('limit')).toBe('5')
  })

  it('always asks lists for a connection and serialises filters as JSON', async () => {
    const { client: flow, calls } = client(() => json({ nodes: [], pageInfo: { hasNextPage: false, hasPreviousPage: false } }))
    await flow.projects({ filter: { status: { type: { eq: 'started' } } }, teamId: 'team_1', includeArchived: true })
    const query = calls[0].query
    expect(query.get('first')).toBe('50')
    expect(query.get('teamId')).toBe('team_1')
    expect(query.get('includeArchived')).toBe('true')
    expect(JSON.parse(query.get('filter')!)).toEqual({ status: { type: { eq: 'started' } } })
  })

  it('tolerates the legacy array response of a list route', async () => {
    const { client: flow } = client(() => json([{ id: 'w1' }]))
    const webhooks = await flow.webhooks()
    expect(webhooks.nodes).toEqual([{ id: 'w1' }])
    expect(webhooks.pageInfo.hasNextPage).toBe(false)
  })

  it('lists comments of an issue', async () => {
    const { client: flow, calls } = client(() => json({ nodes: [{ id: 'c1' }], pageInfo: { hasNextPage: false, hasPreviousPage: false } }))
    const comments = await flow.comments({ issueId: 'ENG-1', first: 5 })
    expect(calls[0].path).toBe('/api/comments')
    expect(calls[0].query.get('issueId')).toBe('ENG-1')
    expect(comments.nodes[0].id).toBe('c1')
  })

  it('finds attachments by URL', async () => {
    const { client: flow, calls } = client(() => json({ nodes: [], pageInfo: { hasNextPage: false, hasPreviousPage: false } }))
    await flow.attachmentsForURL('https://support.example.com/tickets/42')
    expect(calls[0].query.get('url')).toBe('https://support.example.com/tickets/42')
  })

  it('reads the audit log and its types', async () => {
    const { client: flow, calls } = client(request =>
      request.path.endsWith('/types') ? json([{ type: 'customer.created', count: 2 }]) : json({ nodes: [{ id: 'a1' }], pageInfo: { hasNextPage: false, hasPreviousPage: false } }),
    )
    expect((await flow.auditLog({ type: 'customer.created', since: '-P7D' })).nodes).toHaveLength(1)
    expect(calls[0].query.get('since')).toBe('-P7D')
    expect(await flow.auditLogTypes()).toEqual([{ type: 'customer.created', count: 2 }])
  })
})

describe('mutations', () => {
  it('creates and updates issues', async () => {
    const { client: flow, calls } = client(request => json({ ...issue, title: request.json.title }, { status: request.method === 'POST' ? 201 : 200 }))
    const created = await flow.createIssue({ teamId: 'team_1', title: 'Crash', description: 'Steps' })
    expect(calls[0].method).toBe('POST')
    expect(calls[0].path).toBe('/api/issues')
    expect(calls[0].headers.get('content-type')).toBe('application/json')
    expect(calls[0].json).toEqual({ teamId: 'team_1', title: 'Crash', description: 'Steps' })
    expect(created.title).toBe('Crash')
    await flow.updateIssue('ENG-1', { title: 'Renamed', priority: 2 })
    expect(calls[1].method).toBe('PATCH')
    expect(calls[1].path).toBe('/api/issues/ENG-1')
    await flow.archiveIssue('ENG-1')
    expect(calls[2].json).toEqual({ archived: true })
  })

  it('creates and updates comments under their issue', async () => {
    const { client: flow, calls } = client(() => json({ id: 'c1', body: 'x' }, { status: 201 }))
    await flow.createComment({ issueId: 'ENG-1', body: 'Hi', createAsUser: 'Mark' })
    expect(calls[0].path).toBe('/api/issues/ENG-1/comments')
    expect(calls[0].json).toEqual({ body: 'Hi', createAsUser: 'Mark' })
    await flow.updateComment('c1', { issueId: 'ENG-1', body: 'Edited', resolved: true })
    expect(calls[1].method).toBe('PATCH')
    expect(calls[1].path).toBe('/api/issues/ENG-1/comments/c1')
    expect(calls[1].json).toEqual({ body: 'Edited', resolved: true })
  })

  it('upserts link attachments and reports whether one was created', async () => {
    const { client: flow, calls } = client((_, index) => json({ id: 'att_1', url: 'https://x.test/1' }, { status: index === 0 ? 201 : 200 }))
    const first = await flow.upsertLinkAttachment('ENG-1', { url: 'https://x.test/1', title: 'Ticket 1', metadata: { status: 'open' } })
    const second = await flow.upsertLinkAttachment('ENG-1', { url: 'https://x.test/1', title: 'Ticket 1 (closed)' })
    expect(first.created).toBe(true)
    expect(second.created).toBe(false)
    expect(calls[0].path).toBe('/api/issues/ENG-1/link-attachments')
    expect(calls[0].json.metadata).toEqual({ status: 'open' })
    expect(second.attachment.id).toBe('att_1')
  })

  it('updates and deletes attachments', async () => {
    const { client: flow, calls } = client(request => (request.method === 'DELETE' ? new Response(null, { status: 204 }) : json({ id: 'att_1' })))
    await flow.updateAttachment('att_1', { issueId: 'ENG-1', url: 'https://x.test/1', title: 'New' })
    expect(calls[0].path).toBe('/api/issues/ENG-1/attachments/att_1')
    await expect(flow.deleteAttachment('ENG-1', 'att_1')).resolves.toBeUndefined()
    expect(calls[1].method).toBe('DELETE')
  })

  it('upserts customers by external id', async () => {
    const { client: flow, calls } = client((_, index) => json({ id: 'cus_1', externalIds: ['crm-1042'] }, { status: index === 0 ? 201 : 200 }))
    const created = await flow.upsertCustomer({ externalId: 'crm-1042', name: 'Acme', domains: ['acme.com'] })
    const updated = await flow.upsertCustomer({ externalId: 'crm-1042', tier: 'Enterprise' })
    expect(calls[0].method).toBe('PUT')
    expect(calls[0].path).toBe('/api/customers')
    expect(created).toMatchObject({ created: true, customer: { id: 'cus_1' } })
    expect(updated.created).toBe(false)
  })

  it('creates projects, initiatives and customer requests', async () => {
    const { client: flow, calls } = client(() => json({ id: 'x' }, { status: 201 }))
    await flow.createProject({ name: 'Mobile', teamIds: ['team_1'] })
    await flow.updateInitiative('init_1', { name: 'Q4' })
    await flow.createCustomerRequest({ customerExternalId: 'crm-1', issueId: 'ENG-1', attachmentUrl: 'https://x.test/t' })
    expect(calls.map(call => `${call.method} ${call.path}`)).toEqual(['POST /api/projects', 'PATCH /api/initiatives/init_1', 'POST /api/customer-requests'])
  })

  it('manages webhooks', async () => {
    const { client: flow, calls } = client(request => {
      if (request.method === 'DELETE') return new Response(null, { status: 204 })
      if (request.path.endsWith('/deliveries')) return json({ items: [{ id: 'whd_1', status: 'Delivered' }], hasMore: true, nextCursor: 'c1' })
      return json({ id: 'webhook_1', secret: 'shh' })
    })
    expect((await flow.createWebhook({ label: 'CI', url: 'https://ci.example.com/h', resourceTypes: ['Issue'] })).secret).toBe('shh')
    expect(calls[0].json).toEqual({ label: 'CI', url: 'https://ci.example.com/h', resourceTypes: ['Issue'] })
    await flow.updateWebhook('webhook_1', { enabled: false })
    expect(calls[1].method).toBe('PATCH')
    expect((await flow.rotateWebhookSecret('webhook_1')).secret).toBe('shh')
    expect(calls[2].path).toBe('/api/webhooks/webhook_1/rotate-secret')
    await flow.testWebhook('webhook_1')
    await flow.redeliverWebhook('webhook_1', 'whd_1')
    expect(calls[4].path).toBe('/api/webhooks/webhook_1/deliveries/whd_1/redeliver')
    await flow.deleteWebhook('webhook_1')
    const deliveries = await flow.webhookDeliveries('webhook_1', { status: 'failed', first: 10 })
    expect(deliveries.nodes[0].id).toBe('whd_1')
    expect(deliveries.pageInfo).toMatchObject({ hasNextPage: true, endCursor: 'c1' })
    expect(calls[6].query.get('status')).toBe('failed')
    expect(calls[6].query.get('limit')).toBe('10')
  })
})

describe('raw access', () => {
  it('sends any request through client.request', async () => {
    const { client: flow, calls } = client(() => json({ ok: true }))
    const result = await flow.request<{ ok: boolean }>('POST', '/api/issues/ENG-1/share', { body: { a: 1 }, query: { x: ['1', '2'], skip: undefined, when: new Date('2026-01-02T03:04:05Z') } })
    expect(result.ok).toBe(true)
    expect(calls[0].url).toBe(`${API_URL}/api/issues/ENG-1/share?x=1&x=2&when=2026-01-02T03%3A04%3A05.000Z`)
    expect(calls[0].json).toEqual({ a: 1 })
  })

  it('maps errors from raw requests too', async () => {
    const { client: flow } = client(() => envelope('NOT_FOUND', 'issue not found', 404))
    await expect(flow.request('GET', '/api/issues/nope')).rejects.toBeInstanceOf(NotFoundFlowError)
  })

  it('refuses to send credentials to another origin', async () => {
    const { client: flow, calls } = client(() => json({}))
    await expect(flow.request('GET', 'https://evil.example.com/steal')).rejects.toBeInstanceOf(FlowConfigurationError)
    await expect(flow.downloadFile('https://evil.example.com/uploads/a.png')).rejects.toBeInstanceOf(FlowConfigurationError)
    expect(calls).toHaveLength(0)
  })

  it('exposes the generated API', async () => {
    const { client: flow, calls } = client(() => json({ nodes: [], pageInfo: { hasNextPage: false, hasPreviousPage: false } }))
    await flow.api.getTeams({ query: { first: 3, orderBy: 'updatedAt' } })
    expect(calls[0].query.get('orderBy')).toBe('updatedAt')
  })

  it('exposes the last rate-limit state', async () => {
    const seen: number[] = []
    const { client: flow } = client(() => json(issue, { headers: { 'X-RateLimit-Requests-Remaining': '2499', 'X-RateLimit-Requests-Limit': '2500' } }), {
      onRateLimit: info => seen.push(info.requestsRemaining!),
    })
    expect(flow.rateLimit).toBeUndefined()
    await flow.issue('ENG-1')
    expect(flow.rateLimit).toMatchObject({ requestsLimit: 2500, requestsRemaining: 2499 })
    expect(seen).toEqual([2499])
  })
})
