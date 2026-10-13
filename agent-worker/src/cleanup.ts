/**
 * Moves every "QA spike –" item the eval and checks created to the trash (soft delete) and deletes the baseline
 * arm's chat sessions.  CLEANUP_SESSIONS_FILES=base.json[,…] EVAL_COOKIE_FILE=… npx tsx src/cleanup.ts [--dry-run]
 */
import { readFileSync } from 'node:fs'
import { loadConfig } from './config.js'
import { FlowChatClient, FlowMcp } from './flow-client.js'

const PREFIX = 'QA spike –'
const dry = process.argv.includes('--dry-run')
const config = loadConfig()
const mcp = new FlowMcp(config.flowUrl, config.flowToken)
const deleted: string[] = []
const failed: string[] = []

async function del(tool: string, id: string, label: string) {
  if (dry) { deleted.push(`${tool} ${label}`); return }
  try { await mcp.call(tool, { id }); deleted.push(`${tool} ${label}`) } catch (e) { failed.push(`${tool} ${label}: ${(e as Error).message.slice(0, 120)}`) }
}

async function all(tool: string, args: Record<string, unknown>) {
  const items: any[] = []
  let cursor = ''
  for (let page = 0; page < 200; page++) {
    const r = await mcp.call(tool, { ...args, ...(cursor ? { cursor } : {}) })
    items.push(...(r.items ?? []))
    cursor = r.nextCursor ?? ''
    if (!cursor) break
  }
  return items
}

const titled = (x: any) => String(x.title ?? x.name ?? '').startsWith(PREFIX)

// children first so parents' sub-issue lists don't matter
const issues = (await all('list_issues', { query: 'QA spike', limit: 50 })).filter(titled)
issues.sort((a, b) => (b.parentId ? 1 : 0) - (a.parentId ? 1 : 0))
for (const i of issues) await del('delete_issue', i.id, `${i.identifier} ${i.title}`)
for (const d of (await all('list_documents', { query: 'QA spike', limit: 50 })).filter(titled)) await del('delete_document', d.id, d.title)
for (const p of (await all('list_initiatives', { query: 'QA spike', limit: 50 })).filter(titled)) await del('delete_initiative', p.id, p.name)
for (const p of (await all('list_projects', { query: 'QA spike', limit: 50 })).filter(titled)) await del('delete_project', p.id, p.name)
for (const v of (await all('list_views', { query: 'QA spike', limit: 50 })).filter(titled)) await del('delete_view', v.id, v.name)
for (const c of (await all('list_customers', { query: 'QA spike', limit: 50 })).filter(titled)) await del('delete_customer', c.id, c.name)
for (const k of ((await mcp.call('list_agent_skills', {})).items ?? []).filter(titled)) await del('delete_agent_skill', k.id, k.name)
// Loops have no MCP list tool; list them over REST with the session cookie.
if (process.env.EVAL_COOKIE_FILE) {
  const response = await fetch(`${config.flowUrl}/api/loops`, { headers: { cookie: `flow_clone_session=${readFileSync(process.env.EVAL_COOKIE_FILE, 'utf8').trim()}`, 'x-workspace-key': 'dev-workspace' } })
  const body = (await response.json().catch(() => ({}))) as any
  const loops: any[] = Array.isArray(body) ? body : body.items ?? body.loops ?? []
  for (const loop of loops.filter(titled)) await del('delete_loop', loop.id, loop.name)
}

const cookieFile = process.env.EVAL_COOKIE_FILE
if (cookieFile && process.env.CLEANUP_SESSIONS_FILES) {
  const client = new FlowChatClient(config.flowUrl, readFileSync(cookieFile, 'utf8').trim(), 'dev-workspace')
  for (const file of process.env.CLEANUP_SESSIONS_FILES.split(',')) {
    let sessions: string[] = []
    try { sessions = JSON.parse(readFileSync(file, 'utf8')).sessions ?? [] } catch { continue }
    for (const id of sessions) { if (!dry) await client.deleteSession(id); deleted.push(`agent session ${id}`) }
  }
}
console.log(JSON.stringify({ dry, deleted, failed }, null, 2))
