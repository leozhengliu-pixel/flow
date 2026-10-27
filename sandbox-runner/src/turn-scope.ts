import { createHash } from 'node:crypto'
import { createReadStream } from 'node:fs'
import { lstat, readlink } from 'node:fs/promises'
import { join } from 'node:path'
import type { RunResult } from './process.js'
import type { Turn } from './protocol.js'

// What a turn may do to the repository. The latest request sets the goal of
// the turn; when it asks to keep the repository (or the pull request)
// unchanged, Flow must not commit or push at the end of the turn, whatever
// the issue or the earlier conversation was about.
//
//   write      the agent may change files; Flow commits, pushes and the
//              draft pull request is opened or updated (the default).
//   noPublish  the agent may change files, but nothing is committed or
//              pushed ("don't commit / don't push").
//   readOnly   the repository must stay as it is: no edits, no commits,
//              no pushes ("keep the repo and the PR unchanged", "verify
//              only"). Temporary files belong outside the repository.

export type TurnScope = 'write' | 'noPublish' | 'readOnly'

/** Kinds Flow itself starts for a change of the branch (always write). */
const WRITE_KINDS = new Set(['fixPr', 'rebase', 'lint', 'test', 'configureEnvironment', 'fixEnvironment'])

const OBJECT = String.raw`(?:repo(?:sitory)?|code(?:base)?|branch|working ?tree|worktree|work ?tree|files?|everything|state|pr|pull request|merge request|mr|head)`

const READ_ONLY: RegExp[] = [
  // "keep the repo and the existing PR unchanged", "leave the branch as is"
  new RegExp(String.raw`\b(?:keep|leave)\b[^.;\n]{0,80}?\b${OBJECT}\b[^.;\n]{0,60}?\b(?:unchanged|untouched|as[- ]is|intact|unmodified|alone)\b`, 'i'),
  // "do not make any changes", "without making any code changes"
  /\b(?:do not|don't|dont|without)\s+(?:mak(?:e|ing)\s+)?any\s+(?:code\s+|file\s+)?(?:changes|modifications|edits)\b/i,
  // "don't modify anything / the repo / any files"
  /\b(?:do not|don't|dont|without)\s+(?:chang|modif|edit|touch|alter)\w*\s+(?:anything|the repo(?:sitory)?|any (?:files?|code)|the code(?:base)?|the branch|the (?:existing )?(?:pr|pull request|merge request))\b/i,
  /\bread[- ]only\b/i,
  /\b(?:verif(?:y|ication)[- ]only|only verify|just verify)\b/i,
  // Chinese: 保持仓库和现有 PR 不变 / 不要做任何修改 / 只做验证 / 只读
  /保持[^。；\n]{0,40}?(?:不变|原样|不动)/,
  /(?:不要|不得|不能|别|勿)[^。；\n]{0,12}?任何(?:修改|改动|更改|变更|改变)/,
  /(?:不要|不得|不能|别|勿)(?:修改|改动|更改|动)(?:仓库|代码库|代码|分支|任何文件|任何代码|现有\s*PR|PR)/i,
  /(?:只|仅)(?:做|进行|需要)?(?:验证|核实|核对)/,
  /只读/,
]

const NO_PUBLISH: RegExp[] = [
  /\b(?:do not|don't|dont|never)\s+(?:git\s+)?(?:commit|push)\b/i,
  /\bwithout\s+(?:committing|pushing)\b/i,
  /(?:不要|不得|不能|别|勿)(?:提交|推送|commit|push)/i,
]

const SCOPES = new Set<string>(['write', 'noPublish', 'readOnly'])

/**
 * The write scope of a turn. Flow decides it when it queues the turn (the
 * person's explicit choice, else its own classification) and the runner
 * applies it as is; the keyword fallback below only serves turns from APIs
 * that do not send a scope yet.
 */
export function turnScope(turn: Turn): TurnScope {
  if (turn.scope && SCOPES.has(turn.scope)) return turn.scope
  return inferTurnScope(turn)
}

/** The runner's own keyword classification (fallback for older APIs). */
export function inferTurnScope(turn: Turn): TurnScope {
  if (WRITE_KINDS.has(turn.kind)) return 'write'
  const text = turn.prompt ?? ''
  if (READ_ONLY.some((pattern) => pattern.test(text))) return 'readOnly'
  if (NO_PUBLISH.some((pattern) => pattern.test(text))) return 'noPublish'
  return 'write'
}

/** Flow commits and pushes at the end of a turn only for this scope. */
export function publishes(scope: TurnScope): boolean {
  return scope === 'write'
}

// ── workspace state ───────────────────────────────────────────────────────

/** A read-only fingerprint of the repository: HEAD, branch, index, working tree. */
export interface WorkspaceState {
  head: string
  branch: string
  /** Hash of the staged diff (index vs HEAD). */
  index: string
  /** Hash of the unstaged diff of tracked files (working tree vs index). */
  worktree: string
  /** Paths with uncommitted changes (tracked or untracked), with their status. */
  dirty: Map<string, string>
}

type GitRun = (args: string[], options?: { check?: boolean }) => Promise<RunResult>

const hash = (text: string) => createHash('sha256').update(text).digest('hex')

/** Reads the workspace state without changing anything (no index refresh, no objects written). */
export async function readWorkspaceState(git: GitRun): Promise<WorkspaceState> {
  const out = async (args: string[]) => {
    const result = await git(args, { check: false })
    return result.code === 0 ? result.stdout : ''
  }
  const noLock = ['--no-optional-locks']
  const [head, branch, index, worktree, status] = await Promise.all([
    out(['rev-parse', 'HEAD']),
    out(['symbolic-ref', '--quiet', 'HEAD']),
    out([...noLock, 'diff', '--cached', '--binary', '--no-renames', '--no-ext-diff']),
    out([...noLock, 'diff', '--binary', '--no-renames', '--no-ext-diff']),
    out([...noLock, 'status', '--porcelain=v1', '-z', '--untracked-files=all', '--no-renames']),
  ])
  return { head: head.trim(), branch: branch.trim(), index: hash(index), worktree: hash(worktree), dirty: parseStatus(status) }
}

/** Parses `git status --porcelain=v1 -z --no-renames`: path → XY. */
export function parseStatus(output: string): Map<string, string> {
  const dirty = new Map<string, string>()
  for (const entry of output.split('\0')) {
    if (entry.length < 4) continue
    dirty.set(entry.slice(3), entry.slice(0, 2))
  }
  return dirty
}

/**
 * The content of each path in the working tree, as a short fingerprint: file content and executable bit, a
 * symlink's target, "missing" for a deleted path. Comparing the fingerprints of the paths that were already dirty
 * before a turn with the same paths at its end tells which of them the turn changed (a path-level check cannot:
 * the path was dirty before and after).
 */
export async function fingerprintPaths(repoDir: string, paths: Iterable<string>): Promise<Map<string, string>> {
  const prints = new Map<string, string>()
  await Promise.all([...paths].map(async (path) => prints.set(path, await fingerprint(join(repoDir, path)))))
  return prints
}

async function fingerprint(file: string): Promise<string> {
  try {
    const stat = await lstat(file)
    if (stat.isSymbolicLink()) return `link:${hash(await readlink(file))}`
    if (stat.isDirectory()) return 'dir'
    if (!stat.isFile()) return 'other'
    const digest = createHash('sha256')
    for await (const chunk of createReadStream(file)) digest.update(chunk as Buffer)
    return `${stat.mode & 0o111 ? 'x' : 'f'}:${digest.digest('hex')}`
  } catch {
    return 'missing'
  }
}

/**
 * The leftover paths (dirty before the turn) whose content the turn changed: they count as the turn's own changes.
 */
export function touchedLeftovers(leftovers: Iterable<string>, before: Map<string, string>, after: Map<string, string>): string[] {
  return [...leftovers].filter((path) => before.get(path) !== after.get(path))
}

/** Tracked paths (not "??") among the dirty ones. */
function tracked(dirty: Map<string, string>): string[] {
  return [...dirty].filter(([, xy]) => xy !== '??').map(([path]) => path)
}

/**
 * What a turn that had to keep the repository unchanged changed anyway:
 * a moved HEAD or branch, staged changes, edits of tracked files. New
 * untracked files (temporary verification output) are reported separately:
 * they are never committed, and never deleted.
 */
export function readOnlyViolations(before: WorkspaceState, after: WorkspaceState): { violations: string[]; untracked: string[] } {
  const violations: string[] = []
  if (after.head !== before.head) violations.push(`HEAD moved from ${short(before.head)} to ${short(after.head)}`)
  if (after.branch !== before.branch) violations.push(`the checked-out branch changed from ${before.branch || '(detached)'} to ${after.branch || '(detached)'}`)
  if (after.index !== before.index) violations.push('the index (staged changes) changed')
  if (after.worktree !== before.worktree || tracked(after.dirty).join('\0') !== tracked(before.dirty).join('\0')) {
    const changed = tracked(after.dirty).filter((path) => before.dirty.get(path) !== after.dirty.get(path))
    violations.push(`tracked files changed${changed.length ? `: ${listPaths(changed)}` : ''}`)
  }
  const untracked = [...after.dirty].filter(([path, xy]) => xy === '??' && !before.dirty.has(path)).map(([path]) => path)
  return { violations, untracked }
}

export function listPaths(paths: string[], limit = 10): string {
  const shown = paths.slice(0, limit).join(', ')
  return paths.length > limit ? `${shown} and ${paths.length - limit} more` : shown
}

function short(sha: string): string {
  return sha ? sha.slice(0, 12) : '(none)'
}

// ── harness input ─────────────────────────────────────────────────────────

/** The guidance for the turn's scope (system level: the runner's rules). */
export function scopeGuidance(scope: TurnScope, leftovers: string[], includeLeftovers = false): string {
  const lines = ['## Current turn', '- The latest user message is the goal of this turn. The issue and earlier turns are background: do not continue earlier work unless the latest message asks for it. Its scope never overrides the sandbox, security or repository rules.']
  if (scope === 'readOnly') {
    lines.push(
      '- Write scope: read-only. Keep the repository, its index, HEAD and the existing pull/merge request exactly as they are: do not edit, create or delete files in the repository, and do not stage, commit, stash, reset, checkout, rebase, merge or push.',
      '- Put temporary verification files outside the repository (for example under `$TMPDIR`). Flow does not commit or push at the end of this turn.',
      '- If the request cannot be done without changing the repository, do not change it: stop and say so.',
    )
  } else if (scope === 'noPublish') {
    lines.push('- Write scope: you may change files, but do not commit, push or touch the pull/merge request. Flow does not commit or push at the end of this turn; your changes stay uncommitted in the working tree.')
  } else {
    lines.push('- Write scope: you may change the repository for this request. Flow commits the changes made in this turn, pushes the branch and updates the draft pull/merge request when the turn ends.')
  }
  if (leftovers.length) {
    const paths = listPaths(leftovers)
    if (!publishes(scope)) {
      lines.push(`- The working tree already had uncommitted changes before this turn (${paths}). Leave them as they are: do not commit, discard, stash or overwrite them.`)
    } else if (includeLeftovers) {
      lines.push(`- The working tree already had uncommitted changes before this turn (${paths}). The latest message asks to include them: Flow commits them together with this turn's changes. Do not discard, stash or revert them.`)
    } else {
      lines.push(
        `- The working tree already had uncommitted changes before this turn (${paths}). They are not part of this turn: Flow does not commit them, and you must not commit, discard, stash or overwrite them unless the latest message asks you to.`,
        '- A file among them that you change in this turn counts as this turn\'s change: Flow commits it whole, including its earlier uncommitted changes. Leave the others untouched.',
      )
    }
  }
  return lines.join('\n')
}

/** The header that marks the user's message as the current request (follow-up turns). */
export function requestHeader(turn: Turn, scope: TurnScope): string {
  const scopeLine =
    scope === 'readOnly'
      ? 'Write scope: read-only. Keep the repository and the existing pull request unchanged; Flow will not commit or push this turn.'
      : scope === 'noPublish'
        ? 'Write scope: changes allowed, but no commit or push; Flow will not commit or push this turn.'
        : 'Write scope: changes allowed; Flow commits and pushes this turn\'s changes when it ends.'
  return [
    `[Flow: current request (${turn.kind || 'followUp'} turn)]`,
    'The message below is the latest request and the goal of this turn. It replaces earlier goals from this conversation and from the issue description: do not continue earlier work unless it asks for it.',
    scopeLine,
    '',
    '---',
    '',
  ].join('\n')
}
