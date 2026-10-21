import { CommandError, run, type RunOptions, type RunResult } from '../process.js'
import type { BootstrapGit, BootstrapRepo, CodingCommit, CodingDiffSummary } from '../protocol.js'

// git operations of the runner: clone/checkout, identity and signing config,
// commit, push and the diff summary. All network operations authenticate
// through the credential helper; no token is ever written to .git/config.

export type Runner = (command: string, args: string[], options?: RunOptions) => Promise<RunResult>

export interface GitOptions {
  repoDir: string
  env: NodeJS.ProcessEnv
  credentialHelper: string
  signProgram: string
  run?: Runner
}

export class BranchPolicyError extends Error {}

/** Why asking the code host about a branch failed: never read as "the branch does not exist". */
export type RemoteFailure = 'auth' | 'network' | 'unavailable' | 'unknown'

export class RemoteQueryError extends Error {
  constructor(
    readonly kind: RemoteFailure,
    readonly branch: string,
    detail: string,
  ) {
    const what = {
      auth: 'the code host rejected the credentials or denied access',
      network: 'the code host could not be reached',
      unavailable: 'the code host is unavailable',
      unknown: 'the query failed',
    }[kind]
    super(`Could not check whether branch ${branch} exists on origin: ${what}.${detail ? `\n${detail}` : ''}`)
    this.name = 'RemoteQueryError'
  }
}

/** A branch the session expects on origin (pushed before, or a PR's branch) is gone and the workspace has no copy. */
export class BranchNotFoundError extends Error {
  constructor(readonly branch: string) {
    super(`Branch ${branch} was published before but no longer exists on origin, and this workspace has no copy of it. Restore the branch on the code host or start over.`)
    this.name = 'BranchNotFoundError'
  }
}

/** Classifies a failed git network command from its stderr. */
export function classifyRemoteFailure(stderr: string, timedOut = false): RemoteFailure {
  if (/authentication failed|could not read (username|password)|terminal prompts disabled|invalid username or password|returned error: 40[13]|http(\/[\d.]+)? 40[13]|permission denied|access denied|repository not found|not authorized|bad credentials/i.test(stderr)) return 'auth'
  if (/returned error: 5\d\d|http(\/[\d.]+)? 5\d\d|service unavailable|bad gateway|gateway time-?out|internal server error|server is (busy|unavailable)|try again later|rate limit/i.test(stderr)) return 'unavailable'
  if (timedOut || /could not resolve host|connection (refused|reset|timed out)|operation timed out|failed to connect|network is unreachable|no route to host|ssl|tls|gnutls|early eof|unexpected disconnect|rpc failed|remote end hung up|timed out/i.test(stderr)) return 'network'
  return 'unknown'
}

/** How checkoutSessionBranch prepared the branch (for the setup log). */
export interface BranchCheckout {
  /** remote: origin's branch; kept: the workspace's own branch (unpushed work); created: a new branch from `base`. */
  source: 'remote' | 'kept' | 'created'
  base?: string
  note?: string
  /** created again because an unconfirmed existing branch is definitely absent on origin: its unpushed commits are gone. */
  recovered?: boolean
}

/** Non-interactive git environment (no prompts, no pager, no system askpass). */
export function gitEnv(base: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  return {
    ...base,
    GIT_TERMINAL_PROMPT: '0',
    GIT_ASKPASS: '',
    SSH_ASKPASS: '',
    GCM_INTERACTIVE: 'never',
    GIT_PAGER: 'cat',
    GIT_LFS_SKIP_SMUDGE: base.GIT_LFS_SKIP_SMUDGE ?? '0',
  }
}

/** Longest commit subject; longer summary lines are cut with an ellipsis. */
export const MAX_SUBJECT = 72

function summaryLine(summary: string): string | undefined {
  return summary
    .split('\n')
    .map((line) => line.replace(/^[#>*\-\s]+/, '').trim())
    .find((line) => line.length > 0)
}

export function commitMessage(summary: string, trailer?: string, fallback = 'Update from Flow coding session'): string {
  let subject = summaryLine(summary) || fallback
  if (subject.length > MAX_SUBJECT) subject = subject.slice(0, MAX_SUBJECT - 1).trimEnd() + '…'
  return trailer ? `${subject}\n\n${trailer}\n` : `${subject}\n`
}

/**
 * The session title a turn summary suggests: its first line when it is a
 * title-sized line. A long first line is prose, and cutting it would make a
 * worse pull request title than the one the session already has (the issue's).
 */
export function summaryTitle(summary: string): string | undefined {
  const line = summaryLine(summary)
  return line && line.length <= MAX_SUBJECT ? line : undefined
}

/** The branch the runner may push: the prefix, or any branch for existing branches (PR follow-ups). */
export function checkBranchAllowed(repo: BootstrapRepo, fallbackPrefix: string): void {
  if (repo.existingBranch) return
  const prefix = repo.branchPrefix || fallbackPrefix
  if (!repo.branchName.startsWith(prefix)) {
    throw new BranchPolicyError(`refusing to push ${repo.branchName}: branches must start with ${prefix}`)
  }
}

export function parseNumstat(numstat: string, nameStatus: string): CodingDiffSummary[] {
  const statuses = new Map<string, CodingDiffSummary['status']>()
  for (const line of nameStatus.split('\n')) {
    if (!line.trim()) continue
    const [code, ...paths] = line.split('\t')
    const path = paths.at(-1) ?? ''
    const letter = code.charAt(0)
    statuses.set(path, letter === 'A' ? 'added' : letter === 'D' ? 'deleted' : letter === 'R' ? 'renamed' : 'modified')
  }
  const out: CodingDiffSummary[] = []
  for (const line of numstat.split('\n')) {
    if (!line.trim()) continue
    const [add, del, ...rest] = line.split('\t')
    const path = rest.join('\t')
    out.push({
      path,
      additions: add === '-' ? 0 : Number(add) || 0,
      deletions: del === '-' ? 0 : Number(del) || 0,
      status: statuses.get(path) ?? 'modified',
    })
  }
  return out
}

export class Git {
  readonly repoDir: string
  private readonly env: NodeJS.ProcessEnv
  private readonly runImpl: Runner
  private readonly credentialHelper: string
  private readonly signProgram: string

  constructor(options: GitOptions) {
    this.repoDir = options.repoDir
    this.env = gitEnv(options.env)
    this.runImpl = options.run ?? run
    this.credentialHelper = options.credentialHelper
    this.signProgram = options.signProgram
  }

  git(args: string[], options: RunOptions = {}): Promise<RunResult> {
    return this.runImpl('git', args, { cwd: this.repoDir, env: this.env, timeoutMs: 10 * 60_000, ...options })
  }

  private helperArgs(): string[] {
    // An empty value resets inherited helpers, then ours is the only one.
    return ['-c', 'credential.helper=', '-c', `credential.helper=${this.credentialHelper}`, '-c', 'credential.useHttpPath=true']
  }

  async clone(cloneUrl: string, onLine?: RunOptions['onLine']): Promise<void> {
    const parent = this.repoDir.replace(/\/[^/]+\/?$/, '') || '/'
    await this.runImpl('git', [...this.helperArgs(), 'clone', '--no-tags', '--progress', cloneUrl, this.repoDir], {
      cwd: parent,
      env: this.env,
      timeoutMs: 30 * 60_000,
      onLine,
    })
  }

  async isRepo(): Promise<boolean> {
    const result = await this.git(['rev-parse', '--git-dir'], { check: false })
    return result.code === 0
  }

  /**
   * Points the repo's credential helper at Flow and sets origin's push URL
   * (idempotent). A session that pushes as its creator pushes through Flow's
   * git relay (`pushUrl`): the helper gets only a placeholder for it and
   * Flow adds the member's token server side. Fetches keep using the clone
   * URL. Without a push URL any earlier one (a restored workspace) is removed.
   */
  async configureCredentials(pushUrl?: string): Promise<void> {
    await this.git(['config', '--local', '--replace-all', 'credential.helper', this.credentialHelper])
    await this.git(['config', '--local', 'credential.useHttpPath', 'true'])
    if (pushUrl) await this.git(['remote', 'set-url', '--push', 'origin', pushUrl])
    else await this.git(['config', '--local', '--unset-all', 'remote.origin.pushurl'], { check: false })
  }

  async configureIdentity(identity: BootstrapGit): Promise<void> {
    const set = (key: string, value: string) => this.git(['config', '--local', key, value])
    if (identity.authorName) await set('user.name', identity.authorName)
    if (identity.authorEmail) await set('user.email', identity.authorEmail)
    if (identity.signingFormat === 'ssh' && identity.signingPublicKey) {
      await set('gpg.format', 'ssh')
      await set('gpg.ssh.program', this.signProgram)
      const key = identity.signingPublicKey.trim()
      await set('user.signingkey', key.startsWith('ssh-') || key.startsWith('key::') ? key : `key::${key}`)
      await set('commit.gpgsign', 'true')
    } else if (identity.signingFormat === 'openpgp' && identity.signingPublicKey) {
      await set('gpg.format', 'openpgp')
      await set('gpg.program', this.signProgram)
      await set('user.signingkey', identity.signingPublicKey.trim())
      await set('commit.gpgsign', 'true')
    } else {
      await this.git(['config', '--local', 'commit.gpgsign', 'false'])
    }
  }

  async fetch(): Promise<void> {
    await this.git(['fetch', '--no-tags', '--prune', 'origin'], { timeoutMs: 15 * 60_000 })
  }

  /**
   * The branch's head on origin, asked from the code host itself (not from
   * possibly stale remote-tracking refs): undefined only when the host
   * answered that the branch does not exist. Any other failure throws a
   * RemoteQueryError.
   */
  async remoteBranchHead(branch: string): Promise<string | undefined> {
    const result = await this.git([...this.helperArgs(), 'ls-remote', '--exit-code', '--heads', 'origin', `refs/heads/${branch}`], { check: false, timeoutMs: 2 * 60_000 })
    if (result.code === 0) {
      const sha = result.stdout
        .split('\n')
        .map((line) => line.split('\t'))
        .find(([, ref]) => ref === `refs/heads/${branch}`)?.[0]
      if (sha) return sha
    }
    if (result.code === 2 && !result.timedOut) return undefined
    throw new RemoteQueryError(classifyRemoteFailure(result.stderr, result.timedOut), branch, result.stderr.trim().slice(-1000))
  }

  private async revParse(ref: string): Promise<string | undefined> {
    const result = await this.git(['rev-parse', '--verify', '--quiet', `${ref}^{commit}`], { check: false })
    return result.code === 0 ? result.stdout.trim() : undefined
  }

  private async isAncestor(ancestor: string, descendant: string): Promise<boolean> {
    return (await this.git(['merge-base', '--is-ancestor', ancestor, descendant], { check: false })).code === 0
  }

  /**
   * Checks out the session branch. origin's branch when it exists (unless
   * the workspace's copy has commits origin lacks: those are kept); the
   * workspace's own unpublished branch when there is one (a preserved
   * workspace keeps its commits and uncommitted changes); otherwise a new
   * branch from the exact baseline. An expected branch (`existingBranch`)
   * that is gone fails with BranchNotFoundError instead of starting over,
   * and a failed query fails instead of counting as "not found". Nothing is
   * ever pushed here, so origin's branch is never overwritten.
   */
  async checkoutSessionBranch(repo: BootstrapRepo): Promise<BranchCheckout> {
    const branch = repo.branchName
    const remote = await this.remoteBranchHead(branch)
    const local = await this.revParse(`refs/heads/${branch}`)
    let checkout: BranchCheckout
    if (remote) {
      if ((await this.revParse(`refs/remotes/origin/${branch}`)) !== remote) {
        const fetched = await this.git([...this.helperArgs(), 'fetch', '--no-tags', 'origin', `+refs/heads/${branch}:refs/remotes/origin/${branch}`], { check: false, timeoutMs: 15 * 60_000 })
        if (fetched.code !== 0) throw new RemoteQueryError(classifyRemoteFailure(fetched.stderr, fetched.timedOut), branch, fetched.stderr.trim().slice(-1000))
      }
      if (local && local !== remote && !(await this.isAncestor(local, remote))) {
        await this.git(['checkout', branch])
        checkout = { source: 'kept', note: `kept the workspace's ${branch}: it has commits origin/${branch} does not` }
      } else {
        await this.git(['checkout', '-B', branch, `origin/${branch}`])
        checkout = { source: 'remote' }
      }
      await this.git(['branch', '--set-upstream-to', `origin/${branch}`], { check: false })
    } else if (local) {
      await this.git(['checkout', branch])
      checkout = { source: 'kept', note: `kept the workspace's unpublished ${branch}` }
    } else if (repo.existingBranch && !repo.branchUnconfirmed) {
      throw new BranchNotFoundError(branch)
    } else {
      const { base, note } = await this.branchBase(repo)
      await this.git(['checkout', '-b', branch, base])
      checkout = { source: 'created', base, note }
      // origin definitely has no such branch (remoteBranchHead throws on every other outcome) and nothing proves it
      // was ever published: start it again; origin is never touched.
      if (repo.existingBranch) checkout.recovered = true
    }
    return checkout
  }

  /** The exact baseline for a new branch: baseSha when origin still has it, else origin's base ref. */
  private async branchBase(repo: BootstrapRepo): Promise<{ base: string; note?: string }> {
    const fallback = `origin/${repo.baseRef}`
    if (!repo.baseSha) return { base: fallback }
    if (await this.revParse(repo.baseSha)) return { base: repo.baseSha }
    await this.git([...this.helperArgs(), 'fetch', '--no-tags', 'origin', repo.baseSha], { check: false, timeoutMs: 15 * 60_000 })
    if (await this.revParse(repo.baseSha)) return { base: repo.baseSha }
    return { base: fallback, note: `baseline ${repo.baseSha.slice(0, 12)} is not on origin; branching from ${fallback}` }
  }

  baseRevision(repo: BootstrapRepo): string {
    return repo.baseSha || `origin/${repo.baseRef}`
  }

  async head(): Promise<string> {
    const result = await this.git(['rev-parse', 'HEAD'], { check: false })
    return result.code === 0 ? result.stdout.trim() : ''
  }

  async hasChanges(): Promise<boolean> {
    const result = await this.git(['status', '--porcelain', '--untracked-files=all'])
    return result.stdout.trim().length > 0
  }

  /** Stages everything and commits. Returns the new SHA, or "" when there was nothing to commit. */
  async commitAll(message: string, author: BootstrapGit): Promise<string> {
    await this.git(['add', '-A'])
    const staged = await this.git(['diff', '--cached', '--quiet'], { check: false })
    if (staged.code === 0) return ''
    const env: NodeJS.ProcessEnv = { ...this.env }
    if (author.authorName) env.GIT_AUTHOR_NAME = author.authorName
    if (author.authorEmail) env.GIT_AUTHOR_EMAIL = author.authorEmail
    if (author.committerName) env.GIT_COMMITTER_NAME = author.committerName
    if (author.committerEmail) env.GIT_COMMITTER_EMAIL = author.committerEmail
    await this.git(['commit', '--no-verify', '-F', '-'], { env, stdin: message })
    return this.head()
  }

  /** Commits reachable from HEAD but not from `since` (oldest first). */
  async commitsSince(since: string, branch: string, turnId: string | undefined, signed: boolean): Promise<CodingCommit[]> {
    const range = since ? `${since}..HEAD` : 'HEAD'
    const result = await this.git(['log', '--reverse', '--format=%H%x1f%s%x1f%cI', range], { check: false })
    if (result.code !== 0) return []
    return result.stdout
      .split('\n')
      .filter(Boolean)
      .map((line) => {
        const [sha, message, date] = line.split('\x1f')
        return { sha, branch, turnActivityId: turnId, message, signed, createdAt: new Date(date).toISOString() }
      })
  }

  async push(branch: string, options: { forceWithLease?: boolean } = {}): Promise<void> {
    const args = [...this.helperArgs(), 'push', '--porcelain']
    if (options.forceWithLease) args.push('--force-with-lease')
    args.push('origin', `HEAD:refs/heads/${branch}`)
    await this.git(args, { timeoutMs: 15 * 60_000 })
  }

  async diffSummary(base: string): Promise<CodingDiffSummary[]> {
    const numstat = await this.git(['diff', '--no-renames', '--numstat', `${base}...HEAD`], { check: false })
    const names = await this.git(['diff', '--no-renames', '--name-status', `${base}...HEAD`], { check: false })
    if (numstat.code !== 0) return []
    return parseNumstat(numstat.stdout, names.stdout)
  }
}

/** Maps a failed commit to signingFailed when the signing program failed. */
export function isSigningFailure(error: unknown): boolean {
  if (!(error instanceof CommandError)) return false
  return /flow-sign|failed to sign|gpg failed|signing failed|ssh-keygen|signing key/i.test(error.result.stderr + error.result.stdout)
}
