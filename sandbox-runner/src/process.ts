import { spawn, type ChildProcess } from 'node:child_process'
import { createInterface } from 'node:readline'

// Child process helpers. Harnesses run in their own process group so a stop
// (SIGINT, then SIGKILL after the grace period) reaches every descendant.

export interface ExitResult {
  code: number | null
  signal: NodeJS.Signals | null
  timedOut: boolean
}

export interface SpawnOptions {
  cwd?: string
  env?: NodeJS.ProcessEnv
  /** Written to stdin, which is then closed. */
  stdin?: string | Uint8Array
  /** `ulimit -u` for the process tree (0 = unchanged). */
  pidsLimit?: number
  timeoutMs?: number
  onStdoutLine?: (line: string) => void
  onStderrLine?: (line: string) => void
}

export interface RunningProcess {
  readonly pid: number | undefined
  readonly exited: Promise<ExitResult>
  /** SIGINT the group, then SIGKILL after graceMs if it is still running. */
  interrupt(graceMs: number): Promise<ExitResult>
  kill(): void
}

export function spawnProcess(command: string, args: string[], options: SpawnOptions = {}): RunningProcess {
  let file = command
  let argv = args
  if (options.pidsLimit && options.pidsLimit > 0) {
    file = '/bin/sh'
    argv = ['-c', `ulimit -u ${Math.floor(options.pidsLimit)} 2>/dev/null; exec "$0" "$@"`, command, ...args]
  }
  const child: ChildProcess = spawn(file, argv, {
    cwd: options.cwd,
    env: options.env,
    detached: true,
    stdio: ['pipe', 'pipe', 'pipe'],
  })
  let exitedFlag = false
  let timedOut = false
  let timer: NodeJS.Timeout | undefined
  const exited = new Promise<ExitResult>((resolve) => {
    child.once('error', (error) => {
      exitedFlag = true
      if (timer) clearTimeout(timer)
      options.onStderrLine?.(`failed to start ${command}: ${error.message}`)
      resolve({ code: 127, signal: null, timedOut })
    })
    child.once('close', (code, signal) => {
      exitedFlag = true
      if (timer) clearTimeout(timer)
      resolve({ code, signal, timedOut })
    })
  })
  if (child.stdout) {
    const lines = createInterface({ input: child.stdout, crlfDelay: Infinity })
    lines.on('line', (line) => options.onStdoutLine?.(line))
  }
  if (child.stderr) {
    const lines = createInterface({ input: child.stderr, crlfDelay: Infinity })
    lines.on('line', (line) => options.onStderrLine?.(line))
  }
  child.stdin?.on('error', () => {})
  if (options.stdin !== undefined) child.stdin?.end(options.stdin)
  else child.stdin?.end()

  const signalGroup = (signal: NodeJS.Signals) => {
    if (exitedFlag || child.pid === undefined) return
    try {
      process.kill(-child.pid, signal)
    } catch {
      try {
        child.kill(signal)
      } catch {
        // Already gone.
      }
    }
  }

  const interrupt = async (graceMs: number): Promise<ExitResult> => {
    signalGroup('SIGINT')
    let killTimer: NodeJS.Timeout | undefined
    const killed = new Promise<void>((resolve) => {
      killTimer = setTimeout(() => {
        signalGroup('SIGKILL')
        resolve()
      }, graceMs)
    })
    await Promise.race([exited, killed])
    clearTimeout(killTimer)
    return exited
  }

  if (options.timeoutMs && options.timeoutMs > 0) {
    timer = setTimeout(() => {
      timedOut = true
      void interrupt(5000)
    }, options.timeoutMs)
    timer.unref?.()
  }

  return { pid: child.pid, exited, interrupt, kill: () => signalGroup('SIGKILL') }
}

export interface RunResult {
  code: number
  stdout: string
  stderr: string
  timedOut: boolean
}

export class CommandError extends Error {
  constructor(
    readonly command: string,
    readonly result: RunResult,
  ) {
    super(`${command} failed with exit code ${result.code}${result.timedOut ? ' (timed out)' : ''}: ${(result.stderr || result.stdout).trim().slice(-2000)}`)
    this.name = 'CommandError'
  }
}

export interface RunOptions extends Omit<SpawnOptions, 'onStdoutLine' | 'onStderrLine'> {
  /** Throw CommandError on a non-zero exit (default true). */
  check?: boolean
  /** Called with every output line (stdout and stderr), e.g. for setup logs. */
  onLine?: (line: string, stream: 'stdout' | 'stderr') => void
  /** Cap on captured output per stream (bytes, keeps the tail). */
  maxCapture?: number
}

/** Runs a command to completion, capturing output. */
export async function run(command: string, args: string[], options: RunOptions = {}): Promise<RunResult> {
  const max = options.maxCapture ?? 1024 * 1024
  let stdout = ''
  let stderr = ''
  const append = (current: string, line: string) => {
    const next = current + line + '\n'
    return next.length > max ? next.slice(next.length - max) : next
  }
  const proc = spawnProcess(command, args, {
    ...options,
    onStdoutLine: (line) => {
      stdout = append(stdout, line)
      options.onLine?.(line, 'stdout')
    },
    onStderrLine: (line) => {
      stderr = append(stderr, line)
      options.onLine?.(line, 'stderr')
    },
  })
  const exit = await proc.exited
  const result: RunResult = { code: exit.code ?? (exit.signal ? 128 : 1), stdout, stderr, timedOut: exit.timedOut }
  if (options.check !== false && result.code !== 0) throw new CommandError([command, ...args].join(' '), result)
  return result
}
