// A tour of @flow/sdk: querying, mutations, pagination, files, errors and webhooks.
//
//   cd sdk/typescript && npm install && npm run build   # until the package is published
//   FLOW_BASE_URL=https://flow.example.com FLOW_API_KEY=flow_api_... FLOW_TEAM_ID=team_... \
//     npx tsx docs/examples/sdk-quickstart.ts
//
// Set FLOW_WEBHOOK_SECRET to also start a webhook receiver on port 8080.
import { createServer } from 'node:http'
import {
  FlowClient,
  NotFoundFlowError,
  RatelimitedFlowError,
  isFlowError,
} from '@flow/sdk'
import { FlowWebhookClient } from '@flow/sdk/webhooks'

const apiUrl = process.env.FLOW_BASE_URL
const apiKey = process.env.FLOW_API_KEY
const teamId = process.env.FLOW_TEAM_ID
if (!apiUrl || !apiKey || !teamId) throw new Error('Set FLOW_BASE_URL, FLOW_API_KEY and FLOW_TEAM_ID')

const client = new FlowClient({
  apiKey,
  apiUrl,
  // Retry 429 and 5xx with exponential backoff (Retry-After is honoured).
  retry: { retries: 3 },
  timeout: 15_000,
})

// Who am I?
const viewer = await client.viewer
console.log(`Signed in as ${viewer.user.name} (${viewer.authentication}, scopes: ${viewer.scopes?.join(', ')})`)

// Connections: nodes, pageInfo, fetchNext(), and async iteration over every page.
const teams = await client.teams({ first: 25 })
console.log(teams.nodes.map(team => team.key).join(', '))

const recent = await client.issues({ first: 20, orderBy: 'updatedAt' })
console.log(`${recent.nodes.length} of ${recent.totalCount} issues`)
if (recent.pageInfo.hasNextPage) await recent.fetchNext() // appends to recent.nodes

let urgent = 0
for await (const issue of await client.issues({
  first: 100,
  filter: { field: 'priority', operator: 'is', values: ['urgent'] },
})) {
  urgent++
  if (urgent >= 500) break
}
console.log(`${urgent} urgent issues`)

// Mutations.
const issue = await client.createIssue({
  teamId,
  title: 'Customer cannot log in',
  description: 'Reported through the support desk.',
  priority: 2,
})
await client.updateIssue(issue.id, { title: 'Customer cannot log in (SSO)' })
await client.createComment({ issueId: issue.identifier, body: 'Looking into it.' })

// Link attachments are keyed by URL: calling this again updates the same attachment.
const { attachment, created } = await client.upsertLinkAttachment(issue.identifier, {
  url: 'https://support.example.com/tickets/42',
  title: 'Ticket 42',
  subtitle: 'Open · Priority high',
  metadata: { status: 'open' },
})
console.log(`${created ? 'Created' : 'Updated'} attachment ${attachment.id}`)

// Customers are upserted by externalId, so a CRM sync can run repeatedly.
const { customer } = await client.upsertCustomer({ externalId: 'crm-1042', name: 'Acme', domains: ['acme.com'], tier: 'Enterprise' })
console.log(`Customer ${customer.name}`)

// Files: prepare, PUT the bytes, use the asset URL in Markdown.
const upload = await client.uploadFile(new TextEncoder().encode('log line 1\nlog line 2\n'), { filename: 'debug.log', contentType: 'text/plain' })
await client.createComment({ issueId: issue.identifier, body: `[debug.log](${upload.assetUrl})` })
const [signed] = await client.signFileUrls(upload.assetUrl, 3600) // a link that works without credentials for one hour
console.log(`Signed URL expires ${signed.expiresAt}`)

// Errors: switch on the class or on `type`; rate-limit details come with the error.
try {
  await client.issue('ENG-999999')
} catch (error) {
  if (error instanceof NotFoundFlowError) console.log('No such issue', error.requestId)
  else if (error instanceof RatelimitedFlowError) console.log(`Retry in ${error.retryAfter}s`, error.rateLimit?.requestsRemaining)
  else if (isFlowError(error)) console.log(error.type, error.code, error.status)
  else throw error
}

// Webhooks: verify the signature and replay window, then dispatch to typed handlers.
const secret = process.env.FLOW_WEBHOOK_SECRET
if (secret) {
  const webhooks = new FlowWebhookClient(secret)
  webhooks.on('Issue', event => {
    console.log(`${event.action} ${event.data.identifier ?? event.data.id}`)
  })
  webhooks.on('Comment', event => {
    console.log(`comment ${event.action} on ${event.data.issueId}`)
  })
  const handler = webhooks.createNodeHandler()
  createServer((req, res) => {
    if (req.url === '/hooks/flow') void handler(req, res)
    else {
      res.statusCode = 404
      res.end()
    }
  }).listen(8080, () => console.log('Listening for webhooks on :8080/hooks/flow'))
}
