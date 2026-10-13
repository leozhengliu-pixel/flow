import { describe, expect, expectTypeOf, it } from 'vitest'
import { FlowClient, operations, webhookEventTypes, type IssueWebhookEvent, type WebhookEvent, type WebhookEventOf } from '../src/index.js'
import type { UnknownWebhookEvent, WebhookEvent as PackagedWebhookEvent } from '../src/webhook-events.js'
import { API_URL, json, mockFetch } from './helpers.js'

function setup(responder: Parameters<typeof mockFetch>[0] = () => json({ id: 'x' })) {
  const mock = mockFetch(responder)
  return { ...mock, flow: new FlowClient({ apiKey: 'flow_api_test', apiUrl: API_URL, fetch: mock.fetch }) }
}

const noContent = () => new Response(null, { status: 204 })

describe('single-record getters', () => {
  it('reads one record by id', async () => {
    const { flow, calls } = setup()
    await flow.team('ENG')
    await flow.user('me')
    await flow.workflowState('state_1')
    await flow.issueLabel('label_1')
    await flow.cycle('cycle_1')
    await flow.project('mobile-app-1a2b')
    await flow.initiative('init_1')
    await flow.customer('cus_1')
    expect(calls.map(call => `${call.method} ${call.path}`)).toEqual([
      'GET /api/teams/ENG',
      'GET /api/users/me',
      'GET /api/workflow-states/state_1',
      'GET /api/issue-labels/label_1',
      'GET /api/cycles/cycle_1',
      'GET /api/projects/mobile-app-1a2b',
      'GET /api/initiatives/init_1',
      'GET /api/customers/cus_1',
    ])
  })
})

describe('content writes', () => {
  it('creates, updates and deletes documents', async () => {
    const { flow, calls } = setup(request => (request.method === 'DELETE' ? noContent() : json({ id: 'doc_1' }, { status: request.method === 'POST' ? 201 : 200 })))
    await flow.createDocument({ title: 'Runbook', content: '# Steps', projectIds: ['p1'], createAsUser: 'Mark', displayIconUrl: 'https://cdn.example.com/mark.png' })
    await flow.updateDocument('doc_1', { content: 'Edited', expectedVersion: 3, archived: false })
    await flow.deleteDocument('doc_1')
    expect(calls.map(call => `${call.method} ${call.path}`)).toEqual(['POST /api/documents', 'PATCH /api/documents/doc_1', 'DELETE /api/documents/doc_1'])
    expect(calls[0].json).toEqual({ title: 'Runbook', content: '# Steps', projectIds: ['p1'], createAsUser: 'Mark', displayIconUrl: 'https://cdn.example.com/mark.png' })
    expect(calls[1].json).toEqual({ content: 'Edited', expectedVersion: 3, archived: false })
  })

  it('manages project milestones', async () => {
    const { flow, calls } = setup(request => (request.method === 'DELETE' ? noContent() : request.path.endsWith('/reorder') ? json([{ id: 'm2' }, { id: 'm1' }]) : json({ id: 'm1' })))
    await flow.createProjectMilestone('p1', { name: 'Beta', targetDate: '2026-12-01' })
    await flow.updateProjectMilestone('p1', 'm1', { description: 'Ship it' })
    expect((await flow.reorderProjectMilestones('p1', ['m2', 'm1'])).map(milestone => milestone.id)).toEqual(['m2', 'm1'])
    await flow.deleteProjectMilestone('p1', 'm1')
    expect(calls.map(call => `${call.method} ${call.path}`)).toEqual([
      'POST /api/projects/p1/milestones',
      'PATCH /api/projects/p1/milestones/m1',
      'POST /api/projects/p1/milestones/reorder',
      'DELETE /api/projects/p1/milestones/m1',
    ])
    expect(calls[2].json).toEqual({ ids: ['m2', 'm1'] })
  })

  it('posts, edits, lists and deletes project and initiative updates', async () => {
    const { flow, calls } = setup(request => (request.method === 'DELETE' ? noContent() : request.method === 'GET' ? json([{ id: 'u1' }]) : json({ id: 'u1' })))
    expect(await flow.projectUpdates('p1')).toEqual([{ id: 'u1' }])
    await flow.createProjectUpdate('p1', { body: 'On track', health: 'onTrack' })
    await flow.updateProjectUpdate('p1', 'u1', { health: 'atRisk' })
    await flow.deleteProjectUpdate('p1', 'u1')
    expect(await flow.initiativeUpdates('i1')).toEqual([{ id: 'u1' }])
    await flow.createInitiativeUpdate('i1', { body: 'Going', health: 'onTrack' })
    await flow.updateInitiativeUpdate('i1', 'u1', { body: 'Edited' })
    await flow.deleteInitiativeUpdate('i1', 'u1')
    expect(calls.map(call => `${call.method} ${call.path}`)).toEqual([
      'GET /api/projects/p1/updates',
      'POST /api/projects/p1/updates',
      'PATCH /api/projects/p1/updates/u1',
      'DELETE /api/projects/p1/updates/u1',
      'GET /api/initiatives/i1/updates',
      'POST /api/initiatives/i1/updates',
      'PATCH /api/initiatives/i1/updates/u1',
      'DELETE /api/initiatives/i1/updates/u1',
    ])
  })

  it('deletes projects, initiatives, customers and comments', async () => {
    const { flow, calls } = setup(noContent)
    await flow.deleteProject('p1')
    await flow.deleteInitiative('i1')
    await flow.deleteCustomer('cus_1')
    await flow.deleteComment('ENG-1', 'c1')
    expect(calls.map(call => `${call.method} ${call.path}`)).toEqual(['DELETE /api/projects/p1', 'DELETE /api/initiatives/i1', 'DELETE /api/customers/cus_1', 'DELETE /api/issues/ENG-1/comments/c1'])
  })

  it('attributes issues, comments and link attachments to an external person', async () => {
    const { flow, calls } = setup()
    await flow.createIssue({ teamId: 't1', title: 'From CRM', description: '', createAsUser: 'Mark', displayIconUrl: 'https://cdn.example.com/mark.png' })
    await flow.createComment({ issueId: 'ENG-1', body: 'Hi', createAsUser: 'Mark' })
    await flow.upsertLinkAttachment('ENG-1', { url: 'https://x.test/1', createAsUser: 'Mark' })
    expect(calls.map(call => call.json.createAsUser)).toEqual(['Mark', 'Mark', 'Mark'])
  })
})

describe('webhook management', () => {
  it('uses the typed operations', async () => {
    const { flow, calls } = setup(request => {
      if (request.method === 'DELETE' || request.path.endsWith('/revoke-secret')) return noContent()
      if (request.path.endsWith('/failures')) return json([{ id: 'whd_1:1', webhookId: 'w1', executionId: 'whd_1', url: 'https://x.test', responseOrError: 'boom', createdAt: '2026-10-12T00:00:00Z' }])
      if (request.path.includes('/deliveries/')) return json({ id: 'whd_1', status: 'succeeded' })
      return json({ id: 'w1', secret: 'shh', signingStatus: 'active', payloadVersion: 2 })
    })
    const created = await flow.createWebhook({ url: 'https://ci.example.com/h', resourceTypes: ['Issue'], teamIds: ['t1'] })
    expectTypeOf(created.secret).toEqualTypeOf<string>()
    expectTypeOf(created.signingStatus).toEqualTypeOf<'active' | 'rotationRequired' | 'revoked'>()
    await flow.revokeWebhookSecret('w1')
    expect((await flow.webhookFailures('w1'))[0].responseOrError).toBe('boom')
    expect((await flow.webhookDelivery('w1', 'whd_1')).status).toBe('succeeded')
    expect(calls.map(call => `${call.method} ${call.path}`)).toEqual(['POST /api/webhooks', 'POST /api/webhooks/w1/revoke-secret', 'GET /api/webhooks/w1/failures', 'GET /api/webhooks/w1/deliveries/whd_1'])
  })

  it('configures the webhook of an OAuth application', async () => {
    const { flow, calls } = setup(() => json({ applicationId: 'app_1', clientId: 'c', enabled: true, url: 'https://x.test', resourceTypes: ['AgentSessionEvent'], signingEnabled: true, installations: 2, secret: 'shh' }))
    const settings = await flow.setApplicationWebhook('app_1', { enabled: true, url: 'https://x.test', resourceTypes: ['agent_session_events'] })
    expect(settings.secret).toBe('shh')
    await flow.applicationWebhook('app_1')
    await flow.rotateApplicationWebhookSecret('app_1')
    expect(calls.map(call => `${call.method} ${call.path}`)).toEqual(['PUT /api/oauth-applications/app_1/webhook', 'GET /api/oauth-applications/app_1/webhook', 'POST /api/oauth-applications/app_1/webhook/rotate-secret'])
  })

  it('pages the delivery log forward by cursor', async () => {
    const { flow, calls } = setup(request => json({ items: [{ id: request.query.get('cursor') ?? 'first' }], hasMore: !request.query.get('cursor'), nextCursor: 'c1' }))
    const deliveries = await flow.webhookDeliveries('w1', { status: 'failed', first: 1 })
    expect(calls[0].query.get('status')).toBe('failed')
    await deliveries.fetchNext()
    expect(deliveries.nodes.map(delivery => delivery.id)).toEqual(['first', 'c1'])
  })
})

describe('response shapes of list routes', () => {
  it('has a connection method and a legacy method for each route with two shapes', async () => {
    const { flow, calls } = setup(request => (request.query.get('connection') ? json({ nodes: [{ id: 'd1' }], pageInfo: { hasNextPage: false, hasPreviousPage: false } }) : json([{ id: 'd1' }])))
    const connection = await flow.api.getDocuments({ query: { first: 5 } })
    const legacy = await flow.api.getDocumentsLegacy({ query: { q: 'runbook', archived: 'false' } })
    expect(connection.nodes[0].id).toBe('d1')
    expect(legacy[0].id).toBe('d1')
    expect(calls[0].query.get('connection')).toBe('1')
    expect(calls[1].query.get('connection')).toBeNull()
    expect(calls[1].query.get('q')).toBe('runbook')
    expect(operations.getDocumentsLegacy.path).toBe(operations.getDocuments.path)
    expectTypeOf(legacy).toEqualTypeOf<Awaited<ReturnType<typeof flow.api.getDocumentsLegacy>>>()
  })
})

describe('webhook event types', () => {
  it('are generated from the OpenAPI webhooks section', () => {
    expect(webhookEventTypes).toContain('Issue')
    expect(webhookEventTypes).toContain('AgentSessionEvent')
    expect(webhookEventTypes.length).toBeGreaterThanOrEqual(24)
    expectTypeOf<WebhookEventOf<'Issue'>>().toEqualTypeOf<IssueWebhookEvent>()
    expectTypeOf<PackagedWebhookEvent>().toEqualTypeOf<WebhookEvent>()
  })

  it('narrow on type and treat entity fields other than id as optional', () => {
    const event = { type: 'Issue', action: 'update', data: { id: 'issue_1' } } as unknown as WebhookEvent
    if (event.type === 'Issue') {
      expectTypeOf(event.data.id).toEqualTypeOf<string>()
      expectTypeOf(event.data.title).toEqualTypeOf<string | undefined>()
      expectTypeOf(event.action).toEqualTypeOf<'create' | 'update' | 'remove'>()
    }
    // Nested entities are delivered themselves, with their parent id.
    const nested = { type: 'ProjectUpdate', action: 'create', data: { id: 'u1' } } as unknown as WebhookEvent
    if (nested.type === 'ProjectUpdate') {
      expectTypeOf(nested.data.projectId).toEqualTypeOf<string | undefined>()
      expectTypeOf(nested.data.body).toEqualTypeOf<string | undefined>()
    }
    const milestone = { type: 'ProjectMilestone', action: 'update', data: { id: 'm1' } } as unknown as WebhookEvent
    if (milestone.type === 'ProjectMilestone') {
      expectTypeOf(milestone.data.projectId).toEqualTypeOf<string | undefined>()
      expectTypeOf(milestone.data.sortOrder).toEqualTypeOf<number | undefined>() // position in the project; reordering sends updatedFrom.sortOrder
    }
    const initiativeUpdate = { type: 'InitiativeUpdate', action: 'remove', data: { id: 'i1' } } as unknown as WebhookEvent
    if (initiativeUpdate.type === 'InitiativeUpdate') expectTypeOf(initiativeUpdate.data.initiativeId).toEqualTypeOf<string | undefined>()
    const unknown: UnknownWebhookEvent = { type: 'NewThing', action: 'create', createdAt: '', organizationId: '', webhookId: '', webhookTimestamp: 1 }
    expect(unknown.type).toBe('NewThing')
  })
})
