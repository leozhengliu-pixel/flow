/**
 * Summarizes eval results into the design doc's tables.
 *   npx tsx src/report.ts <arm><rep>.json …     (one file per arm and repeat; results.json files also work)
 * Pass rate is shown per repeat (min–max and mean); latency and token figures pool every case of every repeat.
 */
import { readFileSync } from 'node:fs'
import { basename } from 'node:path'
import type { Result } from './eval.js'

type Run = { file: string; results: Result[] }
const runs: Run[] = process.argv.slice(2).map(file => {
  const data = JSON.parse(readFileSync(file, 'utf8'))
  return { file: basename(file), results: (Array.isArray(data) ? data : data.results) as Result[] }
})
const pct = (xs: number[], p: number) => { const s = [...xs].sort((a, b) => a - b); return s.length ? s[Math.min(s.length - 1, Math.ceil((p / 100) * s.length) - 1)] : NaN }
const mean = (xs: number[]) => xs.reduce((a, b) => a + b, 0) / Math.max(1, xs.length)
const sec = (ms: number) => (ms / 1000).toFixed(1)
const arms = [...new Set(runs.flatMap(r => r.results.map(x => x.arm)))]
const rows: Record<string, string[]> = {}
const add = (label: string, value: string) => (rows[label] ??= []).push(value)
for (const arm of arms) {
  const armRuns = runs.filter(r => r.results.some(x => x.arm === arm))
  const rs = armRuns.flatMap(r => r.results.filter(x => x.arm === arm))
  const passes = armRuns.map(r => r.results.filter(x => x.arm === arm && x.check?.pass).length)
  const total = armRuns[0]?.results.filter(x => x.arm === arm).length ?? 0
  const lat = rs.map(r => r.totalMs)
  const first = rs.flatMap(r => (r.firstActivityMs === undefined ? [] : [r.firstActivityMs]))
  const text = rs.flatMap(r => (r.firstTextMs === undefined ? [] : [r.firstTextMs]))
  const calls = rs.reduce((n, r) => n + (r.modelCalls ?? 0), 0)
  const input = rs.reduce((n, r) => n + (r.inputTokens ?? 0), 0)
  const cached = rs.reduce((n, r) => n + (r.cachedTokens ?? 0), 0)
  add('Repeats × cases', `${armRuns.length} × ${total}`)
  add('Pass per repeat (mean)', `${passes.join(', ')} (${((100 * mean(passes)) / Math.max(1, total)).toFixed(1)}%)`)
  add('Cases failing in any repeat', [...new Set(rs.filter(r => !r.check?.pass).map(r => r.id))].join(' ') || '–')
  add('Latency p50 / p90 / p99 (s)', `${sec(pct(lat, 50))} / ${sec(pct(lat, 90))} / ${sec(pct(lat, 99))}`)
  add('Latency mean (s)', sec(mean(lat)))
  add('First activity p50 / p90 (s)', `${sec(pct(first, 50))} / ${sec(pct(first, 90))}`)
  add('First text p50 (s)', sec(pct(text, 50)))
  add('Tool calls per case (errors per case)', `${(rs.reduce((n, r) => n + r.tools.length, 0) / rs.length).toFixed(1)} (${(rs.reduce((n, r) => n + r.toolErrors, 0) / rs.length).toFixed(2)})`)
  add('Model calls per case (failed attempts total)', calls ? `${(calls / rs.length).toFixed(1)} (${rs.reduce((n, r) => n + (r.failedModelCalls ?? 0), 0)})` : 'n/a')
  add('Tools offered per call (mean)', calls ? String(Math.round(mean(rs.filter(r => r.modelCalls).map(r => r.toolsOfferedMean ?? 0)))) : 'n/a')
  add('Prompt tokens per call / per case', calls ? `${(input / calls / 1000).toFixed(1)}k / ${(input / rs.length / 1000).toFixed(1)}k` : 'n/a')
  add('Cache-read share of prompt tokens', calls ? `${Math.round((100 * cached) / Math.max(1, input))}%` : 'n/a')
  add('Output tokens per case (mean)', calls ? String(Math.round(rs.reduce((n, r) => n + (r.outputTokens ?? 0), 0) / rs.length)) : 'n/a')
  add('Turns with parallel tool calls (max in one turn)', calls ? `${rs.reduce((n, r) => n + (r.parallelTurns ?? 0), 0)} (${Math.max(...rs.map(r => r.maxParallel ?? 0))})` : 'n/a')
  add('Parallel calls with lost arguments', calls ? String(rs.reduce((n, r) => n + (r.emptyArgCalls ?? 0), 0)) : 'n/a')
  const s1 = rs.filter(r => r.id === 'S1')
  add('Stop: signal to end (ms, per repeat)', s1.map(r => r.stopLatencyMs ?? '–').join(', ') || 'n/a')
  add('Follow-up after stop', s1.map(r => (r.followUpStatus === 'response' ? 'ok' : r.followUpStatus)).join(', ') || 'n/a')
}
console.log(`| Metric | ${arms.join(' | ')} |\n|---|${arms.map(() => '---').join('|')}|`)
for (const [label, values] of Object.entries(rows)) console.log(`| ${label} | ${values.join(' | ')} |`)
console.log('\nFailures:')
for (const run of runs) for (const r of run.results) if (!r.check?.pass) console.log(`- ${run.file} ${r.id}: ${r.status} ${r.check?.note ?? ''} ${r.body.slice(0, 140).replace(/\s+/g, ' ')}`)
