import { existsSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from 'node:fs'
import { appendFile, readFile, rename, rm, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import type { AgentInputItem, Session } from '@openai/agents'

/**
 * Conversation history outside the process, so any worker can continue a session.
 *
 * Spike backend: one JSONL file per session (append-only; one line per SDK input item) plus one JSON file per
 * suspended run (the serialized RunState). Production swaps the file calls for Flow's internal runtime API
 * (`agent_transcripts`, chunked, keyed by session) behind the same `Session` interface.
 */
/**
 * Where a run waiting on a person keeps its serialized RunState: this worker's files, or Flow
 * (agent_session_states, through the internal runtime API) so any worker resumes it.
 */
export type SuspendedStore = {
  save(state: string, meta: Record<string, unknown>): Promise<void>
  load(): Promise<({ state: string } & Record<string, unknown>) | undefined>
  clear(): Promise<void>
}

export class FileSessionStore {
  constructor(readonly dir: string) {
    mkdirSync(dir, { recursive: true, mode: 0o700 })
  }

  session(sessionId: string): FileSession {
    return new FileSession(this.dir, sessionId)
  }

  /** A run waiting on a human (approval or ask_user), serialized with RunState.toString(). */
  saveSuspended(sessionId: string, state: string, meta: Record<string, unknown>) {
    atomicWrite(this.path(sessionId, 'suspended.json'), JSON.stringify({ ...meta, savedAt: Date.now(), state }))
  }

  loadSuspended(sessionId: string): ({ state: string } & Record<string, unknown>) | undefined {
    const path = this.path(sessionId, 'suspended.json')
    return existsSync(path) ? JSON.parse(readFileSync(path, 'utf8')) : undefined
  }

  clearSuspended(sessionId: string) {
    rmSync(this.path(sessionId, 'suspended.json'), { force: true })
  }

  /** The session's suspended run in this store. */
  suspendedFor(sessionId: string): SuspendedStore {
    return {
      save: async (state, meta) => this.saveSuspended(sessionId, state, meta),
      load: async () => this.loadSuspended(sessionId),
      clear: async () => this.clearSuspended(sessionId),
    }
  }

  private path(sessionId: string, suffix: string) {
    return join(this.dir, `${safeId(sessionId)}.${suffix}`)
  }
}

/**
 * A session's history plus the repairs a stopped or failed run needs (`addMissing`, `closeDanglingCalls`). The
 * file session keeps it on disk; the memory session holds a history Flow rendered for one run (a chat reply's
 * conversation comes with the run payload, api/cmd/server/agent_worker_chat.go).
 */
export abstract class RepairableSession implements Session {
  constructor(protected readonly sessionId: string) {}

  protected abstract read(): Promise<AgentInputItem[]>
  protected abstract write(items: AgentInputItem[]): Promise<void>
  abstract addItems(items: AgentInputItem[]): Promise<void>
  abstract clearSession(): Promise<void>

  async getSessionId() {
    return this.sessionId
  }

  async getItems(limit?: number): Promise<AgentInputItem[]> {
    const items = await this.read()
    return limit === undefined ? items : items.slice(-limit)
  }

  async popItem(): Promise<AgentInputItem | undefined> {
    const items = await this.read()
    const last = items.pop()
    await this.write(items)
    return last
  }

  /**
   * Appends items the run produced that are not in history yet. On abort the SDK keeps only the user's input, so
   * tool calls that already ran (and changed the workspace) would vanish and the follow-up would deny them.
   */
  async addMissing(items: AgentInputItem[]) {
    const history = await this.read()
    const key = (item: AgentInputItem) => (item.type === 'function_call' || item.type === 'function_call_result' ? `${item.type}:${item.callId}` : 'id' in item && item.id ? `id:${item.id}` : JSON.stringify(item))
    const seen = new Set(history.map(key))
    await this.addItems(items.filter(item => item && !seen.has(key(item))))
  }

  /**
   * After a stop, a function call can be in history without its output (the model asked for a tool, the run was
   * aborted first). Providers reject such histories ("No tool output found"), which poisons every follow-up.
   * Close each dangling call with a "cancelled" output so the next prompt continues cleanly.
   */
  async closeDanglingCalls(reason = 'Stopped by the user before a result came back; the call may or may not have completed. Check before retrying.'): Promise<number> {
    const items = await this.read()
    const outputs = new Set(items.flatMap(item => (item.type === 'function_call_result' ? [item.callId] : [])))
    const dangling = items.filter((item): item is Extract<AgentInputItem, { type: 'function_call' }> => item.type === 'function_call' && !outputs.has(item.callId))
    if (!dangling.length) return 0
    await this.addItems(dangling.map(call => ({ type: 'function_call_result', callId: call.callId, name: call.name, status: 'completed', output: { type: 'text', text: reason } }) as AgentInputItem))
    return dangling.length
  }
}

/** History held in memory for one run. */
export class MemorySession extends RepairableSession {
  private items: AgentInputItem[]

  constructor(sessionId: string, items: AgentInputItem[] = []) {
    super(sessionId)
    this.items = [...items]
  }

  protected async read() {
    return [...this.items]
  }

  protected async write(items: AgentInputItem[]) {
    this.items = [...items]
  }

  async addItems(items: AgentInputItem[]) {
    this.items.push(...items)
  }

  async clearSession() {
    this.items = []
  }
}

export class FileSession extends RepairableSession {
  private readonly file: string
  constructor(dir: string, sessionId: string) {
    super(sessionId)
    this.file = join(dir, `${safeId(sessionId)}.jsonl`)
  }

  async addItems(items: AgentInputItem[]) {
    if (!items.length) return
    await appendFile(this.file, items.map(item => JSON.stringify(item)).join('\n') + '\n', { mode: 0o600 })
  }

  async clearSession() {
    await rm(this.file, { force: true })
  }

  // Async I/O: a sync read of a long history blocks every other session on the event loop.
  protected async read(): Promise<AgentInputItem[]> {
    const text = await readFile(this.file, 'utf8').catch((error: NodeJS.ErrnoException) => (error.code === 'ENOENT' ? '' : Promise.reject(error)))
    return text.split('\n').filter(Boolean).map(line => JSON.parse(line) as AgentInputItem)
  }

  protected async write(items: AgentInputItem[]) {
    const temp = `${this.file}.${process.pid}.tmp`
    await writeFile(temp, items.map(item => JSON.stringify(item)).join('\n') + (items.length ? '\n' : ''), { mode: 0o600 })
    await rename(temp, this.file)
  }
}

function atomicWrite(path: string, data: string) {
  const temp = `${path}.${process.pid}.tmp`
  writeFileSync(temp, data, { mode: 0o600 })
  renameSync(temp, path)
}

function safeId(id: string) {
  return id.replace(/[^A-Za-z0-9_.-]/g, '_')
}
