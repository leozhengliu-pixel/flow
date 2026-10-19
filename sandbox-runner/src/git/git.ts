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

export function commitMessage(summary: string, trailer?: string, fallback = 'Update from Flow coding session'): string {
  const firstLine = summary
    .split('\n')
    .map((line) => line.replace(/^[#>*\-\s]+/, '').trim())
    .find((line) => line.length > 0)
  let subject = firstLine || fallback
  if (subject.length > 72) subject = subject.slice(0, 71).trimEnd() + '…'
  return trailer ? `${subject}\n\n${trailer}\n` : `${subject}\n`
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

  /** Points the repo's credential helper at Flow (idempotent). */
  async configureCredentials(): Promise<void> {
    await this.git(['config', '--local', '--replace-all', 'credential.helper', this.credentialHelper])
    await this.git(['config', '--local', 'credential.useHttpPath', 'true'])
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

  async remoteBranchExists(branch: string): Promise<boolean> {
    const result = await this.git(['rev-parse', '--verify', '--quiet', `refs/remotes/origin/${branch}`], { check: false })
    return result.code === 0
  }

  /** Checks out the session branch: the existing remote branch, or a new one from base. */
  async checkoutSessionBranch(repo: BootstrapRepo): Promise<void> {
    const branch = repo.branchName
    if (await this.remoteBranchExists(branch)) {
      await this.git(['checkout', '-B', branch, `origin/${branch}`])
    } else if (repo.existingBranch) {
      throw new CommandError(`git checkout ${branch}`, { code: 1, stdout: '', stderr: `branch ${branch} does not exist on origin`, timedOut: false })
    } else {
      await this.git(['checkout', '-B', branch, this.baseRevision(repo)])
    }
    await this.git(['branch', '--set-upstream-to', `origin/${branch}`], { check: false })
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
