import { spawn } from 'node:child_process'
import { readFileSync, writeSync } from 'node:fs'
import { readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { ensureCliEnv, readCliEnvState } from './cli-env.js'
import { ConfigError, defaultPaths, loadConfig, readToken, type RunnerConfig } from './config.js'
import { baseEnv, RUNTIME_INFO_FILE, type RuntimeInfo } from './environment.js'
import { Git } from './git/git.js'
import { runCredentialHelper } from './git/credential-helper.js'
import { runSign } from './git/sign.js'
import { harnessFor } from './harness/index.js'
import { createLogger, errorMessage, stderrLogger, type Logger } from './log.js'
import { McpServer } from './mcp/server.js'
import { registerBuiltinTools } from './mcp/tools/index.js'
import type { BootstrapResponse } from './protocol.js'
import { RESTORE_FAILED_EXIT, restoreSnapshot } from './restore.js'
import { AuthError, RuntimeClient } from './runtime-client.js'
import { Session } from './session.js'
import { runSnapshot } from './snapshot.js'
import { waitForBinding } from './warm.js'

// flow-runner entry point. The image's ENTRYPOINT is
// `tini -- flow-runner <mode>`; the shims in /opt/flow/bin call the
// helper subcommands (git-credential, sign, mcp, cli-env).

const USAGE = `usage: flow-runner <command>
  session | snapshot | debug | warm | restore   run the sandbox (default: $FLOW_RUNNER_MODE)
  git-credential <get|store|erase>               git credential helper
  sign <ssh-keygen/gpg arguments>                commit signing through Flow
  mcp                                            Flow MCP server (stdio)
  cli-env ensure                                 refresh gh/glab credentials if needed
  version`

const MODES = new Set(['session', 'snapshot', 'debug', 'warm', 'restore'])

function readCurrentTurn(flowDir: string): string | undefined {
  try {
    return readFileSync(join(flowDir, 'current-turn'), 'utf8').trim() || undefined
  } catch {
    return undefined
  }
}

async function readStdin(): Promise<Buffer> {
  const chunks: Buffer[] = []
  for await (const chunk of process.stdin) chunks.push(chunk as Buffer)
  return Buffer.concat(chunks)
}

/** Runtime URL and token file for shims: env first, then ~/.flow/runtime.json. */
export function resolveRuntime(env: NodeJS.ProcessEnv = process.env): RuntimeInfo {
  if (env.FLOW_RUNTIME_URL && env.FLOW_TOKEN_FILE) {
    return { runtimeUrl: env.FLOW_RUNTIME_URL, tokenFile: env.FLOW_TOKEN_FILE, sandboxId: env.FLOW_SANDBOX_ID || '' }
  }
  const file = join(defaultPaths(env).flowDir, RUNTIME_INFO_FILE)
  try {
    const info = JSON.parse(readFileSync(file, 'utf8')) as RuntimeInfo
    return {
      runtimeUrl: env.FLOW_RUNTIME_URL || info.runtimeUrl,
      tokenFile: env.FLOW_TOKEN_FILE || info.tokenFile,
      sandboxId: env.FLOW_SANDBOX_ID || info.sandboxId,
    }
  } catch {
    throw new ConfigError(`FLOW_RUNTIME_URL is not set and ${file} is missing`)
  }
}

function shimClient(logger: Logger): RuntimeClient {
  const info = resolveRuntime()
  return new RuntimeClient({ baseUrl: info.runtimeUrl, token: () => readToken(info.tokenFile), logger })
}

function newGit(config: RunnerConfig, bootstrap?: BootstrapResponse): Git {
  return new Git({
    repoDir: config.paths.repo,
    env: baseEnv(config, bootstrap),
    credentialHelper: join(config.paths.binDir, 'flow-git-credential'),
    signProgram: join(config.paths.binDir, 'flow-sign'),
  })
}

async function runMode(modeArg: string | undefined): Promise<number> {
  const config = loadConfig(process.env, modeArg)
  const logger = createLogger()
  const client = new RuntimeClient({ baseUrl: config.runtimeUrl, token: () => readToken(config.tokenFile), logger })
  logger.info('flow-runner starting', { mode: config.mode, sandboxId: config.sandboxId, size: process.env.FLOW_SANDBOX_SIZE || undefined, pidsLimit: config.pidsLimit || undefined })

  const shutdown = new AbortController()
  let session: Session | undefined
  const onSignal = (signal: NodeJS.Signals) => {
    logger.info('signal received', { signal })
    if (session) session.requestStop({ by: 'Flow', reason: 'signal' })
    else {
      shutdown.abort()
      process.exit(signal === 'SIGTERM' ? 143 : 130)
    }
  }
  process.on('SIGTERM', onSignal)
  process.on('SIGINT', onSignal)

  switch (config.mode) {
    case 'debug': {
      // Debug terminals exec into the sandbox; the runner just keeps it alive.
      await new Promise<void>((resolve) => shutdown.signal.addEventListener('abort', () => resolve()))
      return 0
    }
    case 'restore': {
      // Init container (Kubernetes) or the first step of the Docker entrypoint.
      // Nothing to restore exits 0; a failed restore exits 86 (snapshotFailed).
      try {
        const archiveUrl = process.env.FLOW_ARCHIVE_URL || undefined
        const snapshotId = process.env.FLOW_SNAPSHOT_ID || (archiveUrl ? 'archive' : (await client.bootstrap())?.snapshotId)
        if (!snapshotId) {
          logger.info('no snapshot to restore')
          return 0
        }
        await restoreSnapshot(client, snapshotId, logger, '/', archiveUrl, config.paths)
        return 0
      } catch (error) {
        logger.error('restore failed', { error: errorMessage(error) })
        return RESTORE_FAILED_EXIT
      }
    }
    default:
      break
  }

  let bootstrap: BootstrapResponse | null
  if (config.mode === 'warm') {
    bootstrap = await waitForBinding(client, logger, shutdown.signal)
    if (!bootstrap) return 0
    if (bootstrap.snapshotId && !bootstrap.restored && bootstrap.mode !== 'snapshot') {
      await restoreSnapshot(client, bootstrap.snapshotId, logger, '/', undefined, config.paths)
      bootstrap = { ...bootstrap, restored: true }
    }
  } else {
    bootstrap = await client.bootstrap()
    if (!bootstrap) throw new Error('bootstrap returned no session for a non-warm sandbox')
  }
  if (!config.sandboxId) config.sandboxId = bootstrap.sandboxId

  if (config.mode === 'snapshot' || bootstrap.mode === 'snapshot') {
    return runSnapshot({ config, api: client, bootstrap, git: newGit(config, bootstrap), logger })
  }
  session = new Session({
    config,
    api: client,
    bootstrap,
    git: newGit(config, bootstrap),
    harness: harnessFor(bootstrap.harness.name),
    token: () => readToken(config.tokenFile),
    logger,
  })
  return session.run()
}

async function main(argv: string[]): Promise<number> {
  const [command, ...rest] = argv
  switch (command) {
    case 'git-credential': {
      const stdin = (await readStdin()).toString('utf8')
      try {
        const out = await runCredentialHelper(rest[0] ?? '', stdin, shimClient(stderrLogger()))
        process.stdout.write(out)
        return 0
      } catch (error) {
        process.stderr.write(`flow-git-credential: ${errorMessage(error)}\n`)
        return 1
      }
    }
    case 'sign': {
      let client: RuntimeClient | undefined
      return runSign(rest, {
        sign: (request) => {
          client ??= shimClient(stderrLogger())
          return client.sign(request)
        },
        readStdin,
        readFile: (path) => readFile(path),
        writeFile: (path, data) => writeFile(path, data, { mode: 0o600 }),
        writeStdout: (data) => process.stdout.write(data),
        writeStderr: (data) => process.stderr.write(data),
        writeFd: (fd, data) => {
          writeSync(fd, data)
        },
        passthrough: (program, args) =>
          new Promise((resolve) => {
            const real = program === 'ssh-keygen' ? '/usr/bin/ssh-keygen' : '/usr/bin/gpg'
            const child = spawn(real, args, { stdio: 'inherit' })
            child.once('error', (error) => {
              process.stderr.write(`flow-sign: ${real}: ${error.message}\n`)
              resolve(2)
            })
            child.once('close', (code) => resolve(code ?? 1))
          }),
      })
    }
    case 'mcp': {
      const env = process.env
      let client: RuntimeClient | undefined
      try {
        client = shimClient(stderrLogger())
      } catch {
        client = undefined
      }
      const paths = defaultPaths(env)
      const server = new McpServer({
        client,
        sandboxId: env.FLOW_SANDBOX_ID || '',
        turnId: env.FLOW_TURN_ID || readCurrentTurn(paths.flowDir),
        repoDir: paths.repo,
        flowDir: paths.flowDir,
        browserEnabled: env.FLOW_BROWSER === '1',
      })
      registerBuiltinTools(server)
      await server.serve(process.stdin, process.stdout)
      return 0
    }
    case 'cli-env': {
      if (rest[0] !== 'ensure') {
        process.stderr.write(USAGE + '\n')
        return 2
      }
      try {
        const client = shimClient(stderrLogger())
        const paths = defaultPaths()
        const state = await readCliEnvState(paths.flowDir)
        if (!state?.host) return 0
        await ensureCliEnv({
          credentials: (request) => client.credentials(request),
          flowDir: paths.flowDir,
          home: paths.home,
          host: state.host,
          codeHost: state.codeHost || 'github',
        })
      } catch (error) {
        process.stderr.write(`flow-runner cli-env: ${errorMessage(error)}\n`)
      }
      return 0
    }
    case 'version':
    case '--version':
      process.stdout.write('flow-runner 0.1.0\n')
      return 0
    case 'help':
    case '--help':
    case '-h':
      process.stdout.write(USAGE + '\n')
      return 0
    default:
      if (command !== undefined && !MODES.has(command)) {
        process.stderr.write(USAGE + '\n')
        return 2
      }
      try {
        return await runMode(command)
      } catch (error) {
        if (error instanceof AuthError) {
          createLogger().warn('sandbox token rejected', { status: error.status })
          return 0
        }
        createLogger().error('flow-runner failed', { error: errorMessage(error) })
        return 1
      }
  }
}

main(process.argv.slice(2)).then(
  (code) => {
    process.exitCode = code
    // Long-poll sockets and timers must not keep a finished runner alive.
    if (!['mcp'].includes(process.argv[2] ?? '')) setTimeout(() => process.exit(code), 100).unref()
  },
  (error) => {
    process.stderr.write(`flow-runner: ${errorMessage(error)}\n`)
    process.exit(1)
  },
)
