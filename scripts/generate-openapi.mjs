import { readFile, readdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'

const root = path.resolve(import.meta.dirname, '..')
const serverDir = path.join(root, 'api/cmd/server')
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

routes.sort((left, right) => left.routePath.localeCompare(right.routePath) || left.method.localeCompare(right.method))
const paths = {}
for (const route of routes) {
  const operations = paths[route.routePath] ??= {}
  if (operations[route.method]) continue
  const parameters = [...route.routePath.matchAll(/\{([^}]+)\}/g)].map(match => ({ name: match[1], in: 'path', required: true, schema: { type: 'string' } }))
  operations[route.method] = {
    operationId: operationID(route.method, route.routePath),
    tags: [routeTag(route.routePath)],
    summary: `${route.method.toUpperCase()} ${route.routePath}`,
    ...(parameters.length ? { parameters } : {}),
    responses: {
      default: {
        description: 'Flow API response. See the domain model documentation for resource fields.',
        content: { 'application/json': { schema: {} } },
      },
    },
    'x-flow-source': route.source,
  }
}

const tokenResponse = { type: 'object', properties: {
  access_token: { type: 'string' }, token_type: { const: 'Bearer' }, expires_in: { type: 'integer' },
  scope: { type: 'string' }, refresh_token: { type: 'string' }, actor: { enum: ['app', 'user'] }, subject_user_id: { type: 'string' }, app_user_id: { type: 'string' },
}, required: ['access_token', 'token_type', 'expires_in', 'scope'] }
for (const tokenPath of ['/oauth/token', '/api/oauth/token']) {
  const operation = paths[tokenPath]?.post
  if (!operation) continue
  operation.security = []
  operation.description = 'OAuth authorization-code, refresh-token and opt-in app-actor client-credentials grants. Confidential clients authenticate with HTTP Basic or form fields.'
  operation.requestBody = { required: true, content: { 'application/x-www-form-urlencoded': { schema: {
    type: 'object', required: ['grant_type'], properties: {
      grant_type: { enum: ['authorization_code', 'refresh_token', 'client_credentials'] }, client_id: { type: 'string' }, client_secret: { type: 'string', writeOnly: true },
      code: { type: 'string' }, redirect_uri: { type: 'string', format: 'uri' }, code_verifier: { type: 'string', writeOnly: true }, refresh_token: { type: 'string', writeOnly: true }, scope: { type: 'string' },
    },
  } } } }
  operation.responses['200'] = { description: 'OAuth token response. Access tokens are secrets and should not be logged.', content: { 'application/json': { schema: tokenResponse } } }
}
for (const publicPath of ['/oauth/register', '/oauth/revoke']) {
  if (paths[publicPath]?.post) paths[publicPath].post.security = []
}
const developerToken = paths['/api/oauth-applications/{id}/developer-token']?.post
if (developerToken) {
  developerToken.security = [{ sessionCookie: [] }]
  developerToken.description = 'Requires an interactive workspace owner/admin session. App tokens never inherit the issuing administrator role.'
  developerToken.requestBody = { required: true, content: { 'application/json': { schema: { type: 'object', properties: { actor: { enum: ['app', 'user'], default: 'app' }, scopes: { type: 'array', items: { type: 'string' }, minItems: 1 } } } } } }
  developerToken.responses['200'] = { description: 'One-time developer access token, valid for one hour.', content: { 'application/json': { schema: tokenResponse } } }
}
const rotateSecret = paths['/api/oauth-applications/{id}/rotate-secret']?.post
if (rotateSecret) {
  rotateSecret.security = [{ sessionCookie: [] }]
  rotateSecret.description = 'Rotate the client secret and immediately invalidate existing client-credentials tokens. The new secret is returned once.'
}

const document = {
  openapi: '3.1.0',
  info: {
    title: 'Flow API',
    version: packageJSON.version,
    description: 'Machine-generated route inventory for the Flow HTTP API. Resource schemas remain governed by api/internal/domain/models.go.',
  },
  servers: [{ url: 'http://127.0.0.1:8080' }],
  paths,
  components: {
    securitySchemes: {
      sessionCookie: { type: 'apiKey', in: 'cookie', name: 'flow_clone_session' },
      bearerToken: { type: 'http', scheme: 'bearer' },
    },
  },
  security: [{ sessionCookie: [] }, { bearerToken: [] }],
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
  console.log(`Wrote ${Object.keys(paths).length} paths to ${path.relative(root, outputPath)}`)
}

function routeTag(routePath) {
  const segment = routePath.split('/').filter(Boolean).find(part => part !== 'api' && !part.startsWith('{'))
  return segment || 'root'
}

function operationID(method, routePath) {
  const parts = routePath.split('/').filter(Boolean).filter(part => part !== 'api').map(part => part.replace(/[{}]/g, ''))
  return [method, ...parts].join('_').replace(/[^a-zA-Z0-9_]/g, '_')
}
