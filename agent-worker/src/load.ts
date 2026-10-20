/**
 * Load test: N concurrent sessions in ONE worker process (or N concurrent in-process chats on the Flow API with
 * LOAD_TARGET=baseline), mixed prompts:
 *   ~60% reads, ~25% writes (create a sub-issue), and per LOAD_STOP / LOAD_SUSPEND sessions that are stopped
 *   mid-run (then asked a follow-up) or suspended on an ask_user question (then resumed from the store by a
 *   fresh session object).
 * Reports success, latency p50/p95/p99, first activity, RSS / heap / CPU / event-loop lag of the worker, model
 * call failures by cause (retried or fatal), tool errors, MCP call latency, and the Flow API process's CPU/RSS.
 *
 *   LOAD_N=100 FLOW_WORKER_PROTOCOL=openai-chat LOAD_OUT=out.json npx tsx src/load.ts
 */
import { execFile } from 'node:child_process'
import { readFileSync, writeFileSync } from 'node:fs'
import { monitorEventLoopDelay } from 'node:perf_hooks'
import { promisify } from 'node:util'
import { loadConfig } from './config.js'
import { FlowChatClient, FlowMcp } from './flow-client.js'
import { flowSystemPrompt } from './prompt.js'
import { FlowAgentWorker, type TurnOutcome } from './worker.js'

const run = promisify(execFile)
const n = Number(process.env.LOAD_N ?? 20)
const target = process.env.LOAD_TARGET === 'baseline' ? 'baseline' : 'worker'
const stopShare = Number(process.env.LOAD_STOP ?? 0.05)
const suspendShare = Number(process.env.LOAD_SUSPEND ?? 0.05)
const config = loadConfig({ stateDir: process.env.FLOW_WORKER_STATE_DIR ?? `/tmp/flow-worker-load-${process.pid}` })
const mcp = new FlowMcp(config.flowUrl, config.flowToken)
const tag = `QA spike – load${n}${target === 'baseline' ? 'b' : config.protocol.slice(7, 11)}`
const pct = (xs: number[], p: number) => { const s = [...xs].sort((a, b) => a - b); return s.length ? s[Math.min(s.length - 1, Math.ceil((p / 100) * s.length) - 1)] : NaN }
const mb = (bytes: number) => Math.round(bytes / 1024 / 1024)

const reads = [
  'How many issues are in the DEV team Backlog right now? One sentence.',
  'How many issues are in Triage right now, and which cycles exist?',
  'Find issues that mention CSV or export and list their identifiers and statuses.',
  'Which projects exist and what is the status of each? Short list.',
  'Who is assigned the most open issues in the DEV team?',
]

type Kind = 'read' | 'write' | 'stop' | 'suspend'
const kinds: Kind[] = Array.from({ length: n }, (_, i) => {
  if (i < Math.round(n * stopShare)) return 'stop'
  if (i < Math.round(n * (stopShare + suspendShare))) return 'suspend'
  return i % 3 === 2 ? 'write' : 'read'
})

// Fixtures: one parent for the writes, one parent with two children per suspend session.
const parent = await mcp.call('save_issue', { team: 'DEV', title: `${tag} parent` })
const suspendParents: string[] = []
for (const kind of kinds) {
  if (kind !== 'suspend') continue
  const p = await mcp.call('save_issue', { team: 'DEV', title: `${tag} bulk parent` })
  for (const child of [1, 2]) await mcp.call('save_issue', { team: 'DEV', parentId: p.id, title: `${tag} bulk child ${child}` })
  suspendParents.push(p.identifier)
}
const promptFor = (kind: Kind, i: number) =>
  kind === 'write' ? `Create a sub-issue under ${parent.identifier} titled "${tag} item ${i}" with priority Low.`
    : kind === 'stop' ? `For each of the 10 most recently updated issues, read it with get_issue one at a time and summarize it in one line.`
      : kind === 'suspend' ? `Use ask_user to ask me whether to delete all sub-issues of ${suspendParents.shift()} (options Delete and Keep), then do what I pick.`
        : reads[i % reads.length]

async function apiProcess(): Promise<string | undefined> {
  try {
    const port = new URL(config.flowUrl).port || '80'
    const { stdout } = await run('lsof', ['-tiTCP:' + port, '-sTCP:LISTEN'])
    return stdout.trim().split('\n')[0]
  } catch { return undefined }
}
async function ps(pid?: string) {
  if (!pid) return { rssMb: 0, cpu: 0 }
  try { const { stdout } = await run('ps', ['-o', 'rss=,%cpu=', '-p', pid]); const [r, c] = stdout.trim().split(/\s+/).map(Number); return { rssMb: Math.round(r / 1024), cpu: c } } catch { return { rssMb: 0, cpu: 0 } }
}

type SessionResult = { kind: Kind; status: string; ms: number; firstMs?: number; stopMs?: number; followUp?: string; resumed?: string; toolErrors: number; mcpMs: number[]; failures: string[] }

const apiPid = await apiProcess()
const apiBefore = await ps(apiPid)
const lag = monitorEventLoopDelay({ resolution: 10 })
lag.enable()
let peakRss = process.memoryUsage().rss
let peakHeap = process.memoryUsage().heapUsed
const baselineRss = process.memoryUsage().rss
const apiSamples: { rssMb: number; cpu: number }[] = []
const sampler = setInterval(async () => {
  const m = process.memoryUsage()
  peakRss = Math.max(peakRss, m.rss)
  peakHeap = Math.max(peakHeap, m.heapUsed)
  apiSamples.push(await ps(apiPid))
}, 250)
const cpu0 = process.cpuUsage()
const t0 = Date.now()
const systemPrompt = flowSystemPrompt({ workspace: 'Dev Workspace (teams DEV, ALPHA)', userName: 'Dev User', today: new Date().toISOString().slice(0, 10) })
const worker = target === 'worker' ? new FlowAgentWorker(config) : undefined
const cookie = target === 'baseline' ? readFileSync(process.env.EVAL_COOKIE_FILE ?? '', 'utf8').trim() : ''

async function workerSession(kind: Kind, i: number): Promise<SessionResult> {
  const sessionId = `load-${n}-${i}-${t0}`
  let session = worker!.session({ sessionId, systemPrompt, maxTurns: 12 })
  const started = Date.now()
  const actionAt = new Map<string, number>()
  const result: SessionResult = { kind, status: 'error', ms: 0, toolErrors: 0, mcpMs: [], failures: [] }
  let stopAt = 0
  const sink = (a: Parameters<Parameters<typeof session.send>[1] & {}>[0]) => {
    if (a.type === 'action') {
      actionAt.set(a.activityId, a.at)
      if (kind === 'stop' && !stopAt) setTimeout(() => { stopAt = Date.now(); session.stop() }, 1000)
    }
    if (a.type === 'action.result') {
      const at = actionAt.get(a.activityId)
      if (at && a.action !== 'ask_user' && a.action !== 'load_tools') result.mcpMs.push(a.at - at)
      if (a.isError) result.toolErrors++
    }
  }
  let outcome: TurnOutcome = await session.send(promptFor(kind, i), sink)
  const collect = (o: TurnOutcome) => { for (const call of o.modelCalls) if (call.error) result.failures.push(call.error.slice(0, 160)) }
  collect(outcome)
  result.firstMs = outcome.firstActivityAt ? outcome.firstActivityAt - outcome.startedAt : undefined
  if (kind === 'stop' && stopAt) {
    result.stopMs = outcome.endedAt - stopAt
    const follow = await session.send('What did you do so far? Do not change anything.', sink)
    collect(follow)
    result.followUp = follow.status
  }
  if (kind === 'suspend' && outcome.status === 'awaiting') {
    await session.close()
    session = worker!.session({ sessionId, systemPrompt, maxTurns: 12 }) // dropped and restored from the store
    outcome = await session.resume('Delete', sink)
    collect(outcome)
    result.resumed = outcome.status
  }
  await session.close()
  result.status = outcome.status
  result.ms = Date.now() - started
  return result
}

async function baselineSession(kind: Kind, i: number): Promise<SessionResult> {
  const client = new FlowChatClient(config.flowUrl, cookie, 'dev-workspace')
  const started = Date.now()
  const result: SessionResult = { kind, status: 'error', ms: 0, toolErrors: 0, mcpMs: [], failures: [] }
  let stopAt = 0
  const first = await client.turn(promptFor(kind, i), {
    onEvent: (e, turn) => {
      if (kind === 'stop' && !stopAt && e.type === 'tool.started') setTimeout(() => { stopAt = Date.now(); void client.stop(turn.sessionId) }, 1000)
    },
  })
  result.firstMs = first.firstActivityAt ? first.firstActivityAt - first.startedAt : undefined
  result.toolErrors = first.toolCalls.filter(c => c.isError).length
  let last = first
  if (kind === 'stop' && stopAt) {
    result.stopMs = first.endedAt - stopAt
    last = await client.turn('What did you do so far? Do not change anything.', { sessionId: first.sessionId })
    result.followUp = last.status
  }
  if (kind === 'suspend' && first.pendingQuestion) {
    const picked = first.pendingQuestion.options.find(option => option.label === 'Delete') ?? first.pendingQuestion.options[0]
    last = await client.turn(picked?.label ?? 'Delete', { sessionId: first.sessionId, answer: { partId: first.pendingQuestion.partId, value: picked?.value ?? 'Delete' } })
    result.resumed = last.status
  }
  if (last.status === 'error') result.failures.push(last.body.slice(0, 160))
  result.status = stopAt && kind === 'stop' ? (first.status === 'response' ? 'response' : 'canceled') : last.status === 'response' && kind === 'suspend' && !result.resumed ? 'response' : last.status
  result.ms = Date.now() - started
  if (first.sessionId) sessionIds.push(first.sessionId)
  return result
}
const sessionIds: string[] = []

const results = await Promise.all(kinds.map((kind, i) => (target === 'worker' ? workerSession(kind, i) : baselineSession(kind, i)).catch(error => ({ kind, status: 'harness', ms: Date.now() - t0, toolErrors: 0, mcpMs: [], failures: [`${String(error)} ${String((error as any)?.cause ?? '')}`.slice(0, 200)] }) as SessionResult)))
const wall = Date.now() - t0
const cpu = process.cpuUsage(cpu0)
clearInterval(sampler)
lag.disable()

const ok = (r: SessionResult) => r.kind === 'stop' ? r.status === 'canceled' && r.followUp === 'response' : r.kind === 'suspend' ? (target === 'worker' ? r.resumed === 'response' : r.status === 'response') : r.status === 'response'
const lat = results.filter(ok).map(r => r.ms)
const failureCauses: Record<string, number> = {}
for (const r of results) for (const f of r.failures) {
  const cause = /429|rate/i.test(f) ? '429/rate limit' : /5\d\d|overloaded/i.test(f) ? '5xx/overloaded' : /timeout|timed out/i.test(f) ? 'timeout' : /terminated|ECONN|socket|network|fetch failed/i.test(f) ? 'network' : f.slice(0, 60)
  failureCauses[cause] = (failureCauses[cause] ?? 0) + 1
}
const mcpMs = results.flatMap(r => r.mcpMs)
const summary = {
  target, n, protocol: target === 'worker' ? config.protocol : 'in-process', model: config.model, wallMs: wall,
  ok: results.filter(ok).length, byKind: Object.fromEntries((['read', 'write', 'stop', 'suspend'] as Kind[]).map(k => [k, `${results.filter(r => r.kind === k && ok(r)).length}/${results.filter(r => r.kind === k).length}`])),
  notOk: results.filter(r => !ok(r)).map(r => `${r.kind}:${r.status}${r.followUp ? `/follow=${r.followUp}` : ''}${r.resumed ? `/resumed=${r.resumed}` : ''} ${r.failures[0] ?? ''}`).slice(0, 10),
  latencyMs: { p50: pct(lat, 50), p95: pct(lat, 95), p99: pct(lat, 99), max: Math.max(...lat) },
  firstActivityMs: { p50: pct(results.flatMap(r => r.firstMs ?? []), 50), p95: pct(results.flatMap(r => r.firstMs ?? []), 95) },
  stopMs: { p50: pct(results.flatMap(r => r.stopMs ?? []), 50), max: Math.max(0, ...results.flatMap(r => r.stopMs ?? [])) },
  worker: target === 'worker' ? {
    rssMb: { baseline: mb(baselineRss), peak: mb(peakRss), perSession: Math.round(((peakRss - baselineRss) / n / 1024 / 1024) * 10) / 10 }, peakHeapMb: mb(peakHeap),
    cpu: { totalMs: Math.round((cpu.user + cpu.system) / 1000), perSessionMs: Math.round((cpu.user + cpu.system) / 1000 / n), meanPctOfOneCore: Math.round(((cpu.user + cpu.system) / 1000 / wall) * 100) },
    eventLoopLagMs: { p50: Math.round(lag.percentile(50) / 1e6), p99: Math.round(lag.percentile(99) / 1e6), max: Math.round(lag.max / 1e6) },
  } : undefined,
  modelCallFailures: failureCauses,
  toolErrors: results.reduce((s, r) => s + r.toolErrors, 0),
  mcpCallMs: { count: mcpMs.length, p50: pct(mcpMs, 50), p95: pct(mcpMs, 95), p99: pct(mcpMs, 99) },
  flowApi: { pid: apiPid ? 'found' : 'n/a', rssMbBefore: apiBefore.rssMb, rssMbPeak: Math.max(0, ...apiSamples.map(s => s.rssMb)), cpuPctPeak: Math.max(0, ...apiSamples.map(s => s.cpu)), cpuPctMean: Math.round(apiSamples.reduce((s, x) => s + x.cpu, 0) / Math.max(1, apiSamples.length)) },
}
console.log(JSON.stringify(summary, null, 2))
if (process.env.LOAD_OUT) writeFileSync(process.env.LOAD_OUT, JSON.stringify({ summary, sessionIds }, null, 2))
process.exit(0)
