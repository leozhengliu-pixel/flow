import { chmod, mkdir, rm, writeFile } from 'node:fs/promises'
import { isAbsolute, join, resolve } from 'node:path'
import { shellQuote } from '../cli-env.js'
import type { Git } from './git.js'

// Co-author trailer for commits the agent makes itself (Claude Code, Codex,
// opencode or Pi running `git commit` during a turn). A Flow-owned hooks directory
// inside the git dir is set as core.hooksPath in the session repo's LOCAL
// config. Its prepare-commit-msg appends the trailer; every hook git supports
// has a script there that runs the repository's original hook (husky,
// lefthook, .git/hooks or a configured core.hooksPath) with the same
// arguments and stdin, keeping its exit status. Nothing here is secret.

export const FLOW_HOOKS_DIR = 'flow-hooks'
const ORIGINAL_KEY = 'flow.originalhookspath'

/** Every hook name git can run (githooks(5)). */
export const GIT_HOOK_NAMES = [
  'applypatch-msg',
  'pre-applypatch',
  'post-applypatch',
  'pre-commit',
  'pre-merge-commit',
  'prepare-commit-msg',
  'commit-msg',
  'post-commit',
  'pre-rebase',
  'post-checkout',
  'post-merge',
  'pre-push',
  'pre-receive',
  'update',
  'proc-receive',
  'post-receive',
  'post-update',
  'reference-transaction',
  'push-to-checkout',
  'pre-auto-gc',
  'post-rewrite',
  'sendemail-validate',
  'fsmonitor-watchman',
  'p4-changelist',
  'p4-prepare-changelist',
  'p4-post-changelist',
  'p4-pre-submit',
  'post-index-change',
]

/** A hook that only runs the original (same arguments, stdin and exit status). */
export function delegateScript(original: string, name: string): string {
  return `#!/bin/sh\n# Written by flow-runner: runs the repository's own ${name} hook.\nhook=${shellQuote(join(original, name))}\nif [ -f "$hook" ] && [ -x "$hook" ]; then exec "$hook" "$@"; fi\nexit 0\n`
}

/**
 * prepare-commit-msg: the repository's own hook runs first (its failure
 * blocks the commit and message edits it makes are kept), then the trailer is
 * added unless the same trailer is already there (git compares the token
 * case-insensitively). Merge and squash messages are left alone.
 */
export function prepareCommitMsgScript(original: string, trailer: string): string {
  return `#!/bin/sh
# Written by flow-runner: runs the repository's own prepare-commit-msg hook, then adds the co-author trailer.
hook=${shellQuote(join(original, 'prepare-commit-msg'))}
if [ -f "$hook" ] && [ -x "$hook" ]; then
  "$hook" "$@" || exit $?
fi
case "$2" in merge|squash) exit 0 ;; esac
[ -s "$1" ] || exit 0
git interpret-trailers --in-place --if-exists addIfDifferent --trailer ${shellQuote(trailer)} "$1" || exit 0
exit 0
`
}

async function out(git: Git, args: string[]): Promise<string> {
  const result = await git.git(args, { check: false })
  return result.code === 0 ? result.stdout.trim() : ''
}

/**
 * Installs (or, without a trailer, removes) the co-author hooks. Idempotent:
 * safe on a fresh clone, a restored workspace and a snapshot. Re-running after
 * the repository changed core.hooksPath (e.g. `husky install`) wraps the new
 * location. Returns true when the hooks are installed.
 */
export async function configureCoAuthorHook(git: Git, trailer: string | undefined): Promise<boolean> {
  const gitDir = await out(git, ['rev-parse', '--absolute-git-dir'])
  if (!gitDir || !isAbsolute(gitDir)) return false
  const root = (await out(git, ['rev-parse', '--show-toplevel'])) || git.repoDir
  const flowDir = join(gitDir, FLOW_HOOKS_DIR)
  const defaultHooks = join(resolve(root, (await out(git, ['rev-parse', '--git-common-dir'])) || gitDir), 'hooks')
  const current = await out(git, ['config', '--get', 'core.hooksPath'])
  const ours = current !== '' && resolve(root, current) === flowDir
  const stored = await out(git, ['config', '--local', '--get', ORIGINAL_KEY])
  const original = ours ? stored || defaultHooks : current ? resolve(root, current) : defaultHooks

  if (!trailer || /[\r\n]/.test(trailer)) {
    if (ours) {
      if (original === defaultHooks) await git.git(['config', '--local', '--unset-all', 'core.hooksPath'], { check: false })
      else await git.git(['config', '--local', 'core.hooksPath', original])
      await git.git(['config', '--local', '--unset-all', ORIGINAL_KEY], { check: false })
    }
    await rm(flowDir, { recursive: true, force: true })
    return false
  }

  await rm(flowDir, { recursive: true, force: true })
  await mkdir(flowDir, { recursive: true })
  for (const name of GIT_HOOK_NAMES) {
    const body = name === 'prepare-commit-msg' ? prepareCommitMsgScript(original, trailer) : delegateScript(original, name)
    await writeFile(join(flowDir, name), body)
    await chmod(join(flowDir, name), 0o755)
  }
  await git.git(['config', '--local', ORIGINAL_KEY, original])
  await git.git(['config', '--local', 'core.hooksPath', flowDir])
  return true
}
