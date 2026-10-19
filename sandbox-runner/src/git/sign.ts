import type { SignRequest, SignResponse } from '../protocol.js'

// flow-sign: the signing program git calls for signed commits. The private
// key never enters the sandbox; the payload goes to POST /sign and Flow
// signs it with the user's key (track E).
//
// SSH (`gpg.format=ssh`, `gpg.ssh.program=flow-sign`): git runs
//   flow-sign -Y sign -n git -f <keyfile> [-U] <bufferfile>
// and reads the armored signature from <bufferfile>.sig. Without a buffer
// file, ssh-keygen semantics are stdin → stdout.
// OpenPGP (`gpg.program=flow-sign`): git runs
//   flow-sign --status-fd=2 -bsau <keyid>
// with the payload on stdin; it expects the armored signature on stdout and
// "\n[GNUPG:] SIG_CREATED " on the status fd.
// Everything else (verification: -Y verify / find-principals /
// check-novalidate, gpg --verify) is passed through to the real program.

export type SignInvocation =
  | { kind: 'ssh'; namespace: string; keyFile?: string; bufferFile?: string }
  | { kind: 'openpgp'; keyId?: string; statusFd: number; armor: boolean }
  | { kind: 'passthrough'; program: 'ssh-keygen' | 'gpg' }

export function parseSignArgs(argv: string[]): SignInvocation {
  const yIndex = argv.indexOf('-Y')
  if (yIndex !== -1) {
    if (argv[yIndex + 1] !== 'sign') return { kind: 'passthrough', program: 'ssh-keygen' }
    let namespace = 'git'
    let keyFile: string | undefined
    const positional: string[] = []
    for (let i = 0; i < argv.length; i++) {
      const arg = argv[i]
      if (i === yIndex || i === yIndex + 1) continue
      if (arg === '-n') namespace = argv[++i] ?? namespace
      else if (arg === '-f') keyFile = argv[++i]
      else if (arg === '-O') i++
      else if (arg === '-U' || arg === '-q') continue
      else if (!arg.startsWith('-')) positional.push(arg)
    }
    return { kind: 'ssh', namespace, keyFile, bufferFile: positional.at(-1) }
  }
  let detach = false
  let sign = false
  let armor = false
  let keyId: string | undefined
  let statusFd = -1
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    if (arg.startsWith('--status-fd=')) statusFd = Number(arg.slice('--status-fd='.length))
    else if (arg === '--status-fd') statusFd = Number(argv[++i])
    else if (arg === '--detach-sign') detach = true
    else if (arg === '--sign') sign = true
    else if (arg === '--armor') armor = true
    else if (arg === '--local-user' || arg === '-u') keyId = argv[++i]
    else if (arg.startsWith('--local-user=')) keyId = arg.slice('--local-user='.length)
    else if (/^-[a-zA-Z]+$/.test(arg)) {
      const flags = arg.slice(1)
      if (flags.includes('b')) detach = true
      if (flags.includes('s')) sign = true
      if (flags.includes('a')) armor = true
      if (flags.endsWith('u')) keyId = argv[++i]
    }
  }
  if (detach && sign) return { kind: 'openpgp', keyId, statusFd: Number.isFinite(statusFd) ? statusFd : -1, armor }
  return { kind: 'passthrough', program: 'gpg' }
}

/** A minimal GnuPG status block git accepts (it looks for "\n[GNUPG:] SIG_CREATED "). */
export function gpgStatus(keyId: string | undefined, now: Date): string {
  const fpr = (keyId || '0000000000000000').replace(/^0x/i, '').toUpperCase()
  const ts = Math.floor(now.getTime() / 1000)
  return `[GNUPG:] KEY_CONSIDERED ${fpr} 2\n[GNUPG:] BEGIN_SIGNING H8\n[GNUPG:] SIG_CREATED D 1 8 00 ${ts} ${fpr}\n`
}

export interface SignDeps {
  sign: (request: SignRequest) => Promise<SignResponse>
  readStdin: () => Promise<Buffer>
  readFile: (path: string) => Promise<Buffer>
  writeFile: (path: string, data: string) => Promise<void>
  writeStdout: (data: string) => void
  writeStderr: (data: string) => void
  /** Writes to an arbitrary fd (gpg --status-fd). */
  writeFd: (fd: number, data: string) => void
  /** Runs the real program for verification; returns its exit code. */
  passthrough: (program: 'ssh-keygen' | 'gpg', argv: string[]) => Promise<number>
  now?: () => Date
}

/** Runs flow-sign and returns the exit code. */
export async function runSign(argv: string[], deps: SignDeps): Promise<number> {
  const invocation = parseSignArgs(argv)
  if (invocation.kind === 'passthrough') return deps.passthrough(invocation.program, argv)
  try {
    if (invocation.kind === 'ssh') {
      const payload = invocation.bufferFile ? await deps.readFile(invocation.bufferFile) : await deps.readStdin()
      const { signature } = await deps.sign({ format: 'ssh', payload: payload.toString('base64') })
      if (!signature.includes('BEGIN SSH SIGNATURE')) throw new Error('Flow returned an invalid SSH signature')
      const armored = signature.endsWith('\n') ? signature : signature + '\n'
      if (invocation.bufferFile) await deps.writeFile(`${invocation.bufferFile}.sig`, armored)
      else deps.writeStdout(armored)
      return 0
    }
    const payload = await deps.readStdin()
    const { signature } = await deps.sign({ format: 'openpgp', payload: payload.toString('base64') })
    if (!signature.includes('BEGIN PGP SIGNATURE')) throw new Error('Flow returned an invalid OpenPGP signature')
    deps.writeStdout(signature.endsWith('\n') ? signature : signature + '\n')
    if (invocation.statusFd >= 0) {
      const status = gpgStatus(invocation.keyId, (deps.now ?? (() => new Date()))())
      if (invocation.statusFd === 1) deps.writeStdout(status)
      else if (invocation.statusFd === 2) deps.writeStderr(status)
      else deps.writeFd(invocation.statusFd, status)
    }
    return 0
  } catch (error) {
    deps.writeStderr(`flow-sign: signing through Flow failed: ${error instanceof Error ? error.message : String(error)}\n`)
    return 1
  }
}
