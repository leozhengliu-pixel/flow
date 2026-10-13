/**
 * The worker process loop on Flow's run queue (design doc §8.1, §10): claim runs for free slots (long poll),
 * mint a run token, run the session, heartbeat every 15 s (a `cancel` answer stops the run), finish the run.
 * A crashed worker simply stops heartbeating; Flow's reconciler requeues the run (nothing written) or marks it
 * interrupted (something written), and another worker picks it up.
 *
 *   FLOW_WORKER_SECRET_FILE=… FLOW_WORKER_ID=w1 FLOW_WORKER_SLOTS=8 npx tsx src/queue-worker.ts
 *
 * Spike payload (written by Flow when it enqueues a run): { prompt, systemPrompt? }. History stays in the
 * worker's session store, keyed by the run's session id.
 */
import { formatActivity } from './activities.js'
import { loadConfig, secret } from './config.js'
import { flowSystemPrompt } from './prompt.js'
import { FlowRuntimeClient, type AgentRun, type RunStatus } from './runtime-client.js'
import { FlowAgentWorker, type TurnStatus } from './worker.js'

const config = loadConfig({ flowToken: 'unused: each run mints its own token' })
const slots = Number(process.env.FLOW_WORKER_SLOTS ?? 8)
const workerId = process.env.FLOW_WORKER_ID ?? `worker-${process.pid}`
const client = new FlowRuntimeClient(config.flowUrl, secret('FLOW_WORKER_SECRET'), workerId)
const worker = new FlowAgentWorker(config)
const active = new Map<string, Promise<void>>()
const log = (line: string) => console.log(`${new Date().toISOString()} [${workerId}] ${line}`)
let stopping = false

const statusOf: Record<TurnStatus, RunStatus> = { response: 'done', awaiting: 'awaiting', canceled: 'canceled', error: 'failed' }

async function execute(run: AgentRun) {
  const payload = (run.payload ?? {}) as { prompt?: string; systemPrompt?: string }
  const { token } = await client.token(run.id)
  const session = worker.session({
    sessionId: run.sessionId,
    flowToken: token,
    systemPrompt: payload.systemPrompt ?? flowSystemPrompt({ workspace: run.workspaceKey, userName: 'the requester', today: new Date().toISOString().slice(0, 10) }),
  })
  const heartbeat = setInterval(async () => {
    try {
      const beat = await client.heartbeat(run.id)
      if (beat.cancel) { log(`${run.id} cancel requested`); session.stop() }
    } catch (error) {
      // Lease lost (another worker has the run now): stop working on it.
      log(`${run.id} heartbeat failed: ${(error as Error).message}`)
      session.stop()
    }
  }, 15_000)
  const t0 = Date.now()
  try {
    log(`${run.id} start (attempt ${run.attempts}, session ${run.sessionId})`)
    const outcome = await session.send(payload.prompt ?? '', activity => { if (activity.type !== 'text.delta' && !(activity.type === 'thought' && activity.ephemeral)) log(`${run.id} ${formatActivity(activity, t0).trim()}`) })
    await client.complete(run.id, statusOf[outcome.status], { usage: outcome.usage, ...(outcome.status === 'error' ? { error: outcome.body.slice(0, 2000) } : {}) })
    log(`${run.id} ${outcome.status} in ${Date.now() - t0} ms`)
  } catch (error) {
    log(`${run.id} failed: ${(error as Error).message}`)
    await client.complete(run.id, 'failed', { error: String(error).slice(0, 2000) }).catch(() => {})
  } finally {
    clearInterval(heartbeat)
    await session.close()
  }
}

async function loop() {
  while (!stopping) {
    const free = slots - active.size
    if (free <= 0) {
      await Promise.race(active.values())
      continue
    }
    let runs: AgentRun[] = []
    try {
      runs = (await client.claim({ chat: free }, 5_000)).runs
    } catch (error) {
      log(`claim failed: ${(error as Error).message}`)
      await new Promise(resolve => setTimeout(resolve, 1_000))
    }
    for (const run of runs) {
      const task = execute(run).finally(() => active.delete(run.id))
      active.set(run.id, task)
    }
  }
  await Promise.all(active.values())
}

for (const signal of ['SIGINT', 'SIGTERM'] as const) process.on(signal, () => { stopping = true; log(`${signal}: draining ${active.size} run(s)`) })
log(`claiming up to ${slots} runs from ${config.flowUrl} (${config.protocol} ${config.model})`)
await loop()
