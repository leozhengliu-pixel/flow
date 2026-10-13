/**
 * Concurrency test: N parallel sessions inside ONE worker process (async tasks, no child processes).
 * Measures process RSS (baseline, after MCP connect, peak), CPU, success and latency.
 *   CONC_N=20 FLOW_WORKER_PROTOCOL=openai-responses npx tsx src/concurrency.ts
 */
import { writeFileSync } from 'node:fs'
import { loadConfig } from './config.js'
import { flowSystemPrompt } from './prompt.js'
import { FlowAgentWorker, type TurnOutcome } from './worker.js'

const n = Number(process.env.CONC_N ?? 20)
const prompt = process.env.CONC_PROMPT ?? 'How many issues are in the DEV team Backlog right now? Answer in one sentence.'
const config = loadConfig({ stateDir: process.env.FLOW_WORKER_STATE_DIR ?? `/tmp/flow-worker-conc-${process.pid}` })
const mb = (bytes: number) => Math.round(bytes / 1024 / 1024)
const pct = (xs: number[], p: number) => { const s = [...xs].sort((a, b) => a - b); return s[Math.min(s.length - 1, Math.ceil((p / 100) * s.length) - 1)] }

const worker = new FlowAgentWorker(config)
globalThis.gc?.()
const baseline = process.memoryUsage().rss
const t0 = Date.now()
const systemPrompt = flowSystemPrompt({ workspace: 'Dev Workspace', userName: 'Dev User', today: new Date().toISOString().slice(0, 10) })
const sessions = Array.from({ length: n }, (_, i) => worker.session({ sessionId: `conc-${i}-${t0}`, systemPrompt, maxTurns: 8 }))

let peak = process.memoryUsage().rss
let peakHeap = process.memoryUsage().heapUsed
const sampler = setInterval(() => {
  const m = process.memoryUsage()
  peak = Math.max(peak, m.rss)
  peakHeap = Math.max(peakHeap, m.heapUsed)
}, 100)
const cpu0 = process.cpuUsage()
const start = Date.now()
const toolErrors: string[] = []
const actionArgs = new Map<string, unknown>()
const outcomes: TurnOutcome[] = await Promise.all(sessions.map(s => s.send(prompt, a => {
  if (a.type === 'action') actionArgs.set(a.activityId, a.parameter)
  if (a.type === 'action.result' && a.isError) toolErrors.push(`${a.action}(${JSON.stringify(actionArgs.get(a.activityId))}): ${a.result.slice(0, 160)}`)
})))
const wall = Date.now() - start
const cpu = process.cpuUsage(cpu0)
clearInterval(sampler)
const afterRun = process.memoryUsage().rss
await Promise.all(sessions.map(s => s.close()))

const ok = outcomes.filter(o => o.status === 'response')
const lat = ok.map(o => o.endedAt - o.startedAt)
const first = ok.map(o => (o.firstActivityAt ?? o.endedAt) - o.startedAt)
const calls = outcomes.flatMap(o => o.modelCalls)
const summary = {
  n, protocol: config.protocol, model: config.model, wallMs: wall,
  ok: ok.length, errors: outcomes.filter(o => o.status !== 'response').map(o => o.body.slice(0, 200)),
  latencyMs: { p50: pct(lat, 50), p90: pct(lat, 90), max: Math.max(...lat) },
  firstActivityMs: { p50: pct(first, 50), p90: pct(first, 90) },
  rssMb: { baseline: mb(baseline), peak: mb(peak), afterRun: mb(afterRun), perSessionAtPeak: Math.round((peak - baseline) / n / 1024 / 1024 * 10) / 10 },
  peakHeapMb: mb(peakHeap),
  cpu: { userMs: Math.round(cpu.user / 1000), systemMs: Math.round(cpu.system / 1000), meanPctOfOneCore: Math.round(((cpu.user + cpu.system) / 1000 / wall) * 100), cpuMsPerSession: Math.round((cpu.user + cpu.system) / 1000 / n) },
  modelCalls: calls.length, toolCalls: outcomes.reduce((s, o) => s + o.toolCalls.length, 0), toolErrors: outcomes.reduce((s, o) => s + o.toolCalls.filter(t => t.isError).length, 0), toolErrorSamples: [...new Set(toolErrors)].slice(0, 8),
  answers: ok.map(o => o.body.replace(/\s+/g, ' ').slice(0, 120)),
}
console.log(JSON.stringify(summary, null, 2))
if (process.env.CONC_OUT) writeFileSync(process.env.CONC_OUT, JSON.stringify(summary, null, 2))
