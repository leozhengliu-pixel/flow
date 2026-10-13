/**
 * One-off session from the command line; prints activities as they happen.
 *   npm run cli -- [--session id] [--resume answer] "prompt"
 */
import { randomUUID } from 'node:crypto'
import { parseArgs } from 'node:util'
import { formatActivity } from './activities.js'
import { loadConfig } from './config.js'
import { flowSystemPrompt } from './prompt.js'
import { FlowAgentWorker } from './worker.js'

const { values, positionals } = parseArgs({ allowPositionals: true, options: { session: { type: 'string' }, resume: { type: 'string' } } })
const worker = new FlowAgentWorker(loadConfig())
const session = worker.session({
  sessionId: values.session ?? `cli-${randomUUID()}`,
  systemPrompt: flowSystemPrompt({ workspace: 'Dev Workspace (teams DEV, ALPHA)', userName: 'Dev User', today: new Date().toISOString().slice(0, 10) }),
})
const t0 = Date.now()
const sink = (a: Parameters<typeof formatActivity>[0]) => { if (a.type !== 'text.delta') console.log(formatActivity(a, t0)) }
process.on('SIGINT', () => session.stop())
const outcome = values.resume !== undefined ? await session.resume(values.resume, sink) : await session.send(positionals.join(' '), sink)
console.log(`\n[${outcome.status}] session=${session.sessionId} ${((outcome.endedAt - outcome.startedAt) / 1000).toFixed(1)}s model calls=${outcome.usage.modelCalls}`)
console.log(outcome.body)
await session.close()
