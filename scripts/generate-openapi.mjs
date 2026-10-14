import { execFileSync } from 'node:child_process'
import { readFile, readdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'

// Builds docs/openapi.json (OpenAPI 3.1):
// - the route inventory, scanned from every mux.Handle/HandleFunc in api/cmd/server;
// - typed operations and component schemas for the public contract, reflected
//   from the Go types by `go run ./cmd/openapi-schema` (api/internal/publicapi);
// - shared error, pagination and rate-limit components and security schemes.
// `--check` fails when the committed file is stale.

const root = path.resolve(import.meta.dirname, '..')
const apiDir = path.join(root, 'api')
const serverDir = path.join(apiDir, 'cmd/server')
const outputPath = path.join(root, 'docs/openapi.json')
const packageJSON = JSON.parse(await readFile(path.join(root, 'web/package.json'), 'utf8'))
const routes = []

for (const entry of await readdir(serverDir, { withFileTypes: true })) {
  if (!entry.isFile() || !entry.name.endsWith('.go') || entry.name.endsWith('_test.go')) continue
  const source = await readFile(path.join(serverDir, entry.name), 'utf8')
  for (const match of source.matchAll(/mux\.Handle(?:Func)?\(\s*"([A-Z]+)\s+([^"?]+)(?:\?[^" ]*)?"/g)) {
    routes.push({ method: match[1].toLowerCase(), routePath: match[2], source: entry.name })
  }
}

const contract = JSON.parse(execFileSync('go', ['run', './cmd/openapi-schema'], { cwd: apiDir, encoding: 'utf8', maxBuffer: 64 << 20, env: { ...process.env, GOFLAGS: process.env.GOFLAGS ?? '' } }))

const errorResponse = { $ref: '#/components/responses/Error' }
const internalRoutes = new Set(contract.internalRoutes)
const untypedRoutes = []
routes.sort((left, right) => left.routePath.localeCompare(right.routePath) || left.method.localeCompare(right.method))
const paths = {}
for (const route of routes) {
  const operations = paths[route.routePath] ??= {}
  if (operations[route.method]) continue
  const typed = contract.operations[`${route.method} ${route.routePath}`]
  if (typed) {
    operations[route.method] = { ...typed, 'x-flow-source': route.source }
    continue
  }
  const parameters = [...route.routePath.matchAll(/\{([^}]+)\}/g)].map(match => ({ name: match[1], in: 'path', required: true, schema: { type: 'string' } }))
  if (!internalRoutes.has(`${route.method.toUpperCase()} ${route.routePath}`)) {
    untypedRoutes.push(`${route.method.toUpperCase()} ${route.routePath}`)
  }
  operations[route.method] = {
    operationId: operationID(route.method, route.routePath),
    tags: [routeTag(route.routePath)],
    summary: `${route.method.toUpperCase()} ${route.routePath}`,
    description: 'Internal route of the Flow web app and administration. It is not part of the typed public contract and may change without notice.',
    ...(parameters.length ? { parameters } : {}),
    responses: {
      '2XX': {
        description: 'Success. The response is not described by this document.',
        content: { 'application/json': { schema: {} } },
      },
      default: errorResponse,
    },
    'x-flow-internal': true,
    'x-flow-source': route.source,
  }
}
if (untypedRoutes.length) {
  console.error(`Routes without a contract that are not declared internal (add an Operation in api/internal/publicapi/contract.go or list them in internal_routes.go):\n${untypedRoutes.map(route => `  ${route}`).join('\n')}`)
  process.exit(1)
}
for (const key of Object.keys(contract.operations)) {
  const [method, routePath] = key.split(' ')
  if (!paths[routePath]?.[method]) {
    console.error(`publicapi.Operations lists ${method.toUpperCase()} ${routePath}, but no server route matches it`)
    process.exit(1)
  }
}

const tokenResponse = { type: 'object', properties: {
  access_token: { type: 'string' }, token_type: { const: 'Bearer' }, expires_in: { type: 'integer' },
  scope: { type: 'string' }, refresh_token: { type: 'string' }, actor: { enum: ['app', 'user'] }, subject_user_id: { type: 'string' }, app_user_id: { type: 'string' },
}, required: ['access_token', 'token_type', 'expires_in', 'scope'] }
const oauthError = { description: 'OAuth error (RFC 6749): `error` is the code, `error_description` the message.', content: { 'application/json': { schema: { type: 'object', properties: { error: { type: 'string' }, error_description: { type: 'string' } }, required: ['error'] } } } }
for (const tokenPath of ['/oauth/token', '/api/oauth/token']) {
  const operation = paths[tokenPath]?.post
  if (!operation) continue
  operation.security = []
  operation.tags = ['oauth']
  operation.summary = 'Exchange or refresh an OAuth token'
  operation.description = 'OAuth authorization-code, refresh-token and opt-in app-actor client-credentials grants. Confidential clients authenticate with HTTP Basic or form fields.'
  operation.requestBody = { required: true, content: { 'application/x-www-form-urlencoded': { schema: {
    type: 'object', required: ['grant_type'], properties: {
      grant_type: { enum: ['authorization_code', 'refresh_token', 'client_credentials'] }, client_id: { type: 'string' }, client_secret: { type: 'string', writeOnly: true },
      code: { type: 'string' }, redirect_uri: { type: 'string', format: 'uri' }, code_verifier: { type: 'string', writeOnly: true }, refresh_token: { type: 'string', writeOnly: true }, scope: { type: 'string' },
    },
  } } } }
  operation.responses = { 200: { description: 'OAuth token response. Access tokens are secrets and should not be logged.', content: { 'application/json': { schema: tokenResponse } } }, 400: oauthError, 401: oauthError, 429: { $ref: '#/components/responses/RateLimited' } }
}
if (paths['/api/oauth/token']?.post) {
  paths['/api/oauth/token'].post.deprecated = true
  paths['/api/oauth/token'].post.operationId = 'post_api_oauth_token_legacy'
  paths['/api/oauth/token'].post.description += ' Deprecated alias of POST /oauth/token.'
}
for (const publicPath of ['/oauth/register', '/oauth/revoke']) {
  if (paths[publicPath]?.post) paths[publicPath].post.security = []
}
for (const mcpPath of ['/mcp', '/mcp/readonly']) {
  for (const operation of Object.values(paths[mcpPath] ?? {})) {
    operation.tags = ['mcp']
    operation.description = 'Model Context Protocol (Streamable HTTP, JSON-RPC 2.0). Authenticate with an API key or OAuth access token; see docs/api.md#mcp-server. Errors follow JSON-RPC, not the REST envelope.'
  }
}
const developerToken = paths['/api/oauth-applications/{id}/developer-token']?.post
if (developerToken) {
  developerToken.security = [{ sessionCookie: [] }]
  developerToken.description = 'Requires an interactive workspace owner/admin session. App tokens never inherit the issuing administrator role.'
  developerToken.requestBody = { required: true, content: { 'application/json': { schema: { type: 'object', properties: { actor: { enum: ['app', 'user'], default: 'app' }, scopes: { type: 'array', items: { type: 'string' }, minItems: 1 } } } } } }
  developerToken.responses = { 200: { description: 'One-time developer access token, valid for one hour.', content: { 'application/json': { schema: tokenResponse } } }, default: errorResponse }
}
const rotateSecret = paths['/api/oauth-applications/{id}/rotate-secret']?.post
if (rotateSecret) {
  rotateSecret.security = [{ sessionCookie: [] }]
  rotateSecret.description = 'Rotate the client secret and immediately invalidate existing client-credentials tokens. The new secret is returned once.'
}

// Hand-documented platform routes are part of the public surface.
for (const documented of [['post', '/oauth/token'], ['post', '/api/oauth/token'], ['post', '/oauth/register'], ['post', '/oauth/revoke'], ['post', '/mcp'], ['post', '/mcp/readonly'], ['post', '/api/oauth-applications/{id}/developer-token'], ['post', '/api/oauth-applications/{id}/rotate-secret']]) {
  const operation = paths[documented[1]]?.[documented[0]]
  if (operation) delete operation['x-flow-internal']
}
for (const mcpPath of ['/mcp', '/mcp/readonly']) {
  for (const operation of Object.values(paths[mcpPath] ?? {})) delete operation['x-flow-internal']
}

const rateLimitHeader = (description, format) => ({ description, schema: { type: 'integer', ...(format ? { format } : {}) } })
const errorContent = { 'application/json': { schema: { $ref: '#/components/schemas/Error' } } }
const errorResponseFor = (description, extraHeaders = {}) => ({ description, headers: extraHeaders, content: errorContent })
const scopes = contract.scopes

const document = {
  openapi: '3.1.0',
  info: {
    title: 'Flow API',
    version: packageJSON.version,
    summary: 'REST API of the Flow issue tracker',
    description: [
      'Flow\'s HTTP API. Typed operations (with request and response schemas reflected from the server\'s Go types) form the public contract; every other route is listed as an inventory entry that may change.',
      '',
      'Authenticate with a personal API key or an OAuth access token in `Authorization: Bearer <token>`; tokens select their workspace. Browser sessions send the `X-Workspace-Key` header.',
      'Lists return connections (`nodes`, `pageInfo`) paged with `first`/`after` or `last`/`before`, ordered newest first by `orderBy` (createdAt or updatedAt), filtered with a `filter` object.',
      'Routes marked `x-flow-internal` serve the web app and are not part of the contract. Payloads Flow sends to webhook endpoints are described in the `webhooks` section.',
      'Errors use one envelope (`error`, `code`, `type`, `requestId`). Requests are rate limited per user or application (X-RateLimit-* headers, HTTP 429 with type RATELIMITED).',
      'The API is unversioned: changes are additive, deprecated operations are flagged and kept for at least six months. See docs/api.md.',
    ].join('\n'),
    license: { name: 'Proprietary' },
  },
  servers: [{ url: '{origin}', description: 'Your Flow deployment', variables: { origin: { default: 'http://127.0.0.1:8080', description: 'Scheme, host and port of the Flow server (FLOW_APP_URL).' } } }],
  tags: [
    { name: 'webhooks', description: 'Webhook management, the delivery log, and the payloads Flow sends (see the `webhooks` section of this document and docs/webhooks.md).' },
    { name: 'identity', description: 'Who the caller is and which workspace they act in.' },
    { name: 'files', description: 'Uploads, downloads and signed file URLs.' },
    { name: 'attachments', description: 'File and link attachments on issues.' },
    { name: 'customers', description: 'Customers and customer requests (needs).' },
    { name: 'audit', description: 'Workspace audit log.' },
    { name: 'oauth', description: 'OAuth 2.0 authorization server.' },
    { name: 'oauth-applications', description: 'OAuth applications of the workspace: configuration, manifests, installs and credentials (workspace admins or the admin scope; see docs/oauth.md).' },
    { name: 'mcp', description: 'Model Context Protocol server.' },
  ],
  paths,
  webhooks: contract.webhooks,
  components: {
    schemas: contract.schemas,
    parameters: {
      first: { name: 'first', in: 'query', description: 'Page size counting forward from `after` (1-250, default 50). Alias: limit.', schema: { type: 'integer', minimum: 1, maximum: 250, default: 50 } },
      after: { name: 'after', in: 'query', description: 'Return nodes after this cursor (pageInfo.endCursor). Alias: cursor.', schema: { type: 'string' } },
      last: { name: 'last', in: 'query', description: 'Page size counting backward from `before` (1-250).', schema: { type: 'integer', minimum: 1, maximum: 250 } },
      before: { name: 'before', in: 'query', description: 'Return nodes before this cursor (pageInfo.startCursor).', schema: { type: 'string' } },
      connection: { name: 'connection', in: 'query', description: 'Set to `1` to get the connection form with default paging. Routes that predate connections answer with their original array or page unless first, last, after, before, orderBy or connection=1 is given.', schema: { type: 'string', enum: ['1', 'true'] } },
      includeArchived: { name: 'includeArchived', in: 'query', description: 'Include archived resources.', schema: { type: 'boolean', default: false } },
    },
    headers: {
      'X-RateLimit-Requests-Limit': rateLimitHeader('Requests allowed per hour for this credential.'),
      'X-RateLimit-Requests-Remaining': rateLimitHeader('Requests left in the bucket.'),
      'X-RateLimit-Requests-Reset': rateLimitHeader('When the request bucket is full again (UTC epoch milliseconds).', 'int64'),
      'X-Complexity': rateLimitHeader('Complexity points this request cost.'),
      'X-RateLimit-Complexity-Limit': rateLimitHeader('Complexity points allowed per hour.'),
      'X-RateLimit-Complexity-Remaining': rateLimitHeader('Complexity points left in the bucket.'),
      'X-RateLimit-Complexity-Reset': rateLimitHeader('When the complexity bucket is full again (UTC epoch milliseconds).', 'int64'),
      'Retry-After': rateLimitHeader('Seconds to wait before retrying.'),
    },
    responses: {
      Error: errorResponseFor('Error envelope.'),
      InvalidInput: errorResponseFor('INVALID_INPUT: the request was malformed or failed validation.'),
      AuthenticationError: errorResponseFor('AUTHENTICATION_ERROR: missing, invalid or expired credentials.'),
      Forbidden: errorResponseFor('FORBIDDEN or FEATURE_NOT_ACCESSIBLE: the caller may not access this resource.'),
      NotFound: errorResponseFor('NOT_FOUND: the resource does not exist or is not visible to the caller.'),
      RateLimited: errorResponseFor('RATELIMITED: a request, complexity or endpoint budget is exhausted.', { 'Retry-After': { $ref: '#/components/headers/Retry-After' } }),
    },
    securitySchemes: {
      sessionCookie: { type: 'apiKey', in: 'cookie', name: 'flow_clone_session', description: 'First-party browser session (send X-Workspace-Key).' },
      bearerApiKey: { type: 'http', scheme: 'bearer', description: 'Personal API key (flow_api_…) or OAuth access token (flow_oauth_…).' },
      oauth2: {
        type: 'oauth2',
        description: 'OAuth 2.0 with PKCE. `actor=app` on the authorize URL installs an application actor; client credentials issue app tokens.',
        flows: {
          authorizationCode: { authorizationUrl: '/oauth/authorize', tokenUrl: '/oauth/token', refreshUrl: '/oauth/token', scopes },
          clientCredentials: { tokenUrl: '/oauth/token', scopes },
        },
      },
    },
  },
  security: [{ sessionCookie: [] }, { bearerApiKey: [] }, { oauth2: ['read'] }],
}

const output = `${JSON.stringify(document, null, 2)}\n`
if (process.argv.includes('--check')) {
  const current = await readFile(outputPath, 'utf8').catch(() => '')
  if (current !== output) {
    console.error('docs/openapi.json is stale. Run: node scripts/generate-openapi.mjs')
    process.exit(1)
  }
} else {
  await writeFile(outputPath, output)
  console.log(`Wrote ${Object.keys(paths).length} paths (${Object.keys(contract.operations).length} typed operations, ${Object.keys(contract.schemas).length} schemas) to ${path.relative(root, outputPath)}`)
}

function routeTag(routePath) {
  const segment = routePath.split('/').filter(Boolean).find(part => part !== 'api' && !part.startsWith('{'))
  return segment || 'root'
}

function operationID(method, routePath) {
  const parts = routePath.split('/').filter(Boolean).filter(part => part !== 'api').map(part => part.replace(/[{}]/g, ''))
  return [method, ...parts].join('_').replace(/[^a-zA-Z0-9_]/g, '_')
}
