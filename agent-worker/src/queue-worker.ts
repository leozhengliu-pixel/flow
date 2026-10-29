/**
 * The worker process loop on Flow's run queue (design doc §8.1, §10): claim runs for free slots (long poll),
 * mint a run token, run the session, watch for stops (one long poll per worker; a stop arrives within ~50 ms),
 * heartbeat every 15 s (its `cancel` answer is the fallback), finish the run.
 * A crashed worker simply stops heartbeating; Flow's reconciler requeues the run (nothing written) or marks it
 * interrupted (something written), and another worker picks it up.
 *
 *   FLOW_WORKER_SECRET_FILE=… FLOW_WORKER_ID=w1 FLOW_WORKER_SLOTS=8 npx tsx src/queue-worker.ts
 *
 * A chat run (payload `chat`, queued by Flow for a chat message when FLOW_AGENT_RUNTIME=worker) runs from the
 * conversation Flow serves with the run and writes its reply on Flow (chat-run.ts); a run suspended on held calls
 * keeps its state in Flow and is continued by a resume run. Other runs (spike payload { prompt, systemPrompt? })
 * keep their history in the worker's session store, keyed by the run's session id.
 */
import { formatActivity, type FlowActivity } from './activities.js'
import { ChatActivityPoster, chatConversation } from './chat-run.js'
import { ThreadActivityPoster } from './thread-run.js'
import { toolGroups } from './tool-groups.js'
import { loadConfig, secret } from './config.js'
import { flowSystemPrompt } from './prompt.js'
import { CancelWatcher, FlowRuntimeClient, type AgentRun, type RunStatus } from './runtime-client.js'
import { MemorySession, type SuspendedStore } from './session-store.js'
import { FlowAgentWorker, type FlowAgentSession, type TurnStatus } from './worker.js'

const config = loadConfig({ flowToken: 'unused: each run mints its own token' })
const slots = Number(process.env.FLOW_WORKER_SLOTS ?? 8)
const workerId = process.env.FLOW_WORKER_ID ?? `worker-${process.pid}`
const client = new FlowRuntimeClient(config.flowUrl, secret('FLOW_WORKER_SECRET'), workerId)
const worker = new FlowAgentWorker(config)
const active = new Map<string, Promise<void>>()
const sessions = new Map<string, FlowAgentSession>()
const log = (line: string) => console.log(`${new Date().toISOString()} [${workerId}] ${line}`)
const cancels = new CancelWatcher(client, () => sessions.keys(), runId => {
  log(`${runId} cancel requested`)
  sessions.get(runId)?.stop()
})
let stopping = false

const statusOf: Record<TurnStatus, RunStatus> = { response: 'done', awaiting: 'awaiting', canceled: 'canceled', error: 'failed' }

/** A chat run's suspended RunState lives in Flow (agent_session_states), so any worker resumes it. */
function flowSuspendedStore(runId: string): SuspendedStore {
  return {
    save: async (state, meta) => client.putState(runId, JSON.stringify({ ...meta, savedAt: Date.now(), state })),
    load: async () => {
      const raw = await client.getState(runId)
      return raw ? JSON.parse(raw) : undefined
    },
    clear: async () => client.deleteState(runId).catch(() => {}),
  }
}

async function execute(run: AgentRun) {
  const payload = (run.payload ?? {}) as { prompt?: string; systemPrompt?: string }
  const { token } = await client.token(run.id)
  // Reading the payload discovers the run's connector tools; a skipped selected connector comes back as a notice.
  const details = await client.payload(run.id).catch(error => { log(`${run.id} payload failed: ${(error as Error).message}`); return undefined })
  for (const notice of details?.notices ?? []) log(`${run.id} notice: ${notice}`)
  // A chat reply (chat-run.ts): its conversation comes from Flow, and its activities write the reply on Flow.
  const chat = run.kind === 'chat' ? details?.chat : undefined
  if (run.kind === 'chat' && (run.payload as { chat?: unknown } | undefined)?.chat && !chat) {
    await client.complete(run.id, 'failed', { error: 'The chat run payload could not be read.' }).catch(() => {})
    return
  }
  // A built-in session run (thread-run.ts): Flow renders the thread and queued the run when it delivered the session.
  const thread = run.kind === 'thread' || run.kind === 'delegation' ? details?.thread : undefined
  // A loop run (api/cmd/server/agent_worker_loop.go): Flow lists the loop's tools, runs each call through its gates
  // and records the run; the worker runs the model loop.
  const loop = run.kind === 'loop' ? details?.loop : undefined
  if (loop && details?.failure) {
    await client.complete(run.id, 'failed', { error: details.failure.slice(0, 2000) }).catch(() => {})
    return
  }
  const flowRun = chat ?? thread ?? loop
  const conversation = flowRun ? chatConversation(details?.context?.messages ?? []) : undefined
  let session: FlowAgentSession | undefined
  const onError = (error: unknown) => log(`${run.id} activity failed: ${(error as Error).message}`)
  const poster: { push(activity: FlowActivity): void; drain(): Promise<void> } | undefined = chat
    ? new ChatActivityPoster(activity => client.activity(run.id, activity), {
      onError,
      // The reply was settled on Flow (stopped, edited away): the run stops too.
      onSettled: () => session?.stop(),
    })
    : thread
      ? new ThreadActivityPoster(activity => client.activity(run.id, activity), {
        attempt: run.attempts,
        onError,
        // The session no longer takes the run's activities (stopped, or waiting for the person): the run stops too.
        onRejected: () => session?.stop(),
      })
      : undefined
  session = worker.session({
    sessionId: run.sessionId,
    flowToken: token,
    guard: request => client.guard(run.id, request),
    // Flow shows a chat run's connector questions itself.
    elicitations: chat ? undefined : (after, waitMs, signal) => client.elicitations(run.id, after, waitMs, signal),
    systemPrompt: details?.context?.system ?? payload.systemPrompt ?? flowSystemPrompt({ workspace: run.workspaceKey, userName: 'the requester', today: new Date().toISOString().slice(0, 10) }),
    ...(flowRun ? {
      // The run's tool turns, plus the turn that gives its last word once the budget is spent.
      maxTurns: Math.max(flowRun.maxTurns, 1) + 2,
      // A built-in session run is offered every Flow tool, as executeBuiltinApplication offers them (no chat subset).
      groups: chat ? chat.groups : toolGroups.map(group => group.name),
      history: new MemorySession(run.sessionId, conversation?.history ?? []),
      suspended: flowSuspendedStore(run.id),
    } : {}),
    // A loop run's calls run one at a time, as in-process, and finish_run ends it.
    ...(loop ? { allTools: true, serialCalls: true, stopAtTools: ['finish_run'], elicitations: undefined } : {}),
  })
  sessions.set(run.id, session)
  cancels.started()
  const heartbeat = setInterval(async () => {
    try {
      const beat = await client.heartbeat(run.id)
      if (beat.cancel) { log(`${run.id} cancel requested (heartbeat)`); session.stop() }
    } catch (error) {
      // Lease lost (another worker has the run now): stop working on it.
      log(`${run.id} heartbeat failed: ${(error as Error).message}`)
      session.stop()
    }
  }, 15_000)
  const t0 = Date.now()
  // A loop run has its wall-clock limit (FLOW_LOOP_RUN_TIMEOUT).
  let timedOut = false
  const timeout = loop?.timeoutMs ? setTimeout(() => { timedOut = true; log(`${run.id} timed out`); session.stop() }, Math.max(loop.timeoutMs - (Date.now() - loop.started), 0)) : undefined
  try {
    log(`${run.id} start (attempt ${run.attempts}, session ${run.sessionId}${chat ? `, chat reply ${chat.messageId}` : thread || loop ? `, ${run.kind}` : ''}${flowRun?.resume ? ' resumed' : ''})`)
    const sink = (activity: FlowActivity) => {
      poster?.push(activity)
      if (activity.type !== 'text.delta' && !(activity.type === 'thought' && activity.ephemeral)) log(`${run.id} ${formatActivity(activity, t0).trim()}`)
    }
    const outcome = flowRun?.resume ? await session.resumeWith(flowRun.resume.decisions, sink) : await session.send(conversation ? conversation.request : payload.prompt ?? '', sink, conversation?.input)
    await poster?.drain()
    let status = statusOf[outcome.status]
    if (timedOut && status === 'canceled') status = 'failed'
    if (flowRun && outcome.status === 'awaiting' && outcome.elicitation?.kind === 'select') {
      // ask_user ends a chat reply (or a thread run) with its question; the answer is the person's next message (a new
      // run).
      await flowSuspendedStore(run.id).clear()
      status = 'done'
    }
    await client.complete(run.id, status, { usage: outcome.usage, ...(outcome.status === 'error' ? { error: outcome.body.slice(0, 2000) } : {}) })
    log(`${run.id} ${outcome.status} in ${Date.now() - t0} ms`)
  } catch (error) {
    log(`${run.id} failed: ${(error as Error).message}`)
    await poster?.drain()
    await client.complete(run.id, 'failed', { error: String(error).slice(0, 2000) }).catch(() => {})
  } finally {
    clearInterval(heartbeat)
    if (timeout) clearTimeout(timeout)
    sessions.delete(run.id)
    cancels.finished(run.id)
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
      runs = (await client.claim({ chat: free, thread: free, loop: free }, 5_000, free)).runs
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
  cancels.close()
}

for (const signal of ['SIGINT', 'SIGTERM'] as const) process.on(signal, () => { stopping = true; log(`${signal}: draining ${active.size} run(s)`) })
log(`claiming up to ${slots} runs from ${config.flowUrl} (${config.protocol} ${config.model})`)
void cancels.run()
await loop()
