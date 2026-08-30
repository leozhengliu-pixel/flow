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
      sessionCookie: { type: 'apiKey', in: 'cookie', name: 'flow_session' },
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
