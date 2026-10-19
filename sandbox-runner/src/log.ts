// Structured runner log (the Pod log, archived by Flow on stop). Never pass
// tokens, credentials or secret values to these functions.

export type LogLevel = 'debug' | 'info' | 'warn' | 'error'

export interface Logger {
  debug(msg: string, fields?: Record<string, unknown>): void
  info(msg: string, fields?: Record<string, unknown>): void
  warn(msg: string, fields?: Record<string, unknown>): void
  error(msg: string, fields?: Record<string, unknown>): void
}

const levels: Record<LogLevel, number> = { debug: 10, info: 20, warn: 30, error: 40 }

export function createLogger(
  write: (line: string) => void = (line) => process.stdout.write(line + '\n'),
  minLevel: LogLevel = (process.env.FLOW_RUNNER_LOG_LEVEL as LogLevel) || 'info',
): Logger {
  const min = levels[minLevel] ?? levels.info
  const emit = (level: LogLevel, msg: string, fields?: Record<string, unknown>) => {
    if (levels[level] < min) return
    write(JSON.stringify({ time: new Date().toISOString(), level, msg, ...fields }))
  }
  return {
    debug: (msg, fields) => emit('debug', msg, fields),
    info: (msg, fields) => emit('info', msg, fields),
    warn: (msg, fields) => emit('warn', msg, fields),
    error: (msg, fields) => emit('error', msg, fields),
  }
}

/** A logger that writes to stderr only: shims must keep stdout for git. */
export const stderrLogger = (): Logger => createLogger((line) => process.stderr.write(line + '\n'), 'warn')

export const silentLogger: Logger = { debug() {}, info() {}, warn() {}, error() {} }

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}
