import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { chmodSync, existsSync, mkdirSync, readFileSync, realpathSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { test } from 'node:test'
import { configureCoAuthorHook, FLOW_HOOKS_DIR } from '../src/git/coauthor-hook.js'
import { commitMessage, Git } from '../src/git/git.js'
import { tempDir } from './helpers.js'

const TRAILER = 'Co-Authored-By: flow-app[bot] <42+flow-app[bot]@users.noreply.github.com>'

function setup(prefix: string) {
  const tmp = tempDir(prefix)
  const env = { PATH: process.env.PATH ?? '/usr/bin:/bin', HOME: tmp.path, GIT_CONFIG_NOSYSTEM: '1', GIT_AUTHOR_NAME: 'Ada', GIT_AUTHOR_EMAIL: 'ada@x', GIT_COMMITTER_NAME: 'bot', GIT_COMMITTER_EMAIL: 'bot@x' }
  tmp.path = realpathSync(tmp.path)
  const repoDir = join(tmp.path, 'repo')
  mkdirSync(repoDir)
  const sh = (args: string[], options: { allowFail?: boolean } = {}) => {
    const result = spawnSync('git', args, { cwd: repoDir, env, encoding: 'utf8' })
    if (result.status !== 0 && !options.allowFail) throw new Error(`git ${args.join(' ')} failed (${result.status}): ${result.stderr}`)
    return result
  }
  sh(['init', '-q', '-b', 'main'])
  writeFileSync(join(repoDir, 'a.txt'), 'a\n')
  sh(['add', '.'])
  sh(['commit', '-q', '-m', 'init'])
  const git = new Git({ repoDir, env, credentialHelper: 'h', signProgram: 's' })
  const body = (rev = 'HEAD') => sh(['log', '-1', '--format=%B', rev]).stdout.replace(/\n$/, '')
  const count = (rev = 'HEAD') => body(rev).toLowerCase().split('co-authored-by: flow-app[bot]').length - 1
  let n = 0
  const change = () => writeFileSync(join(repoDir, 'a.txt'), `change ${++n}\n`)
  const hook = (path: string, script: string) => {
    mkdirSync(join(path, '..'), { recursive: true })
    writeFileSync(path, `#!/bin/sh\n${script}\n`)
    chmodSync(path, 0o755)
  }
  return { tmp, env, repoDir, sh, git, body, count, change, hook }
}

test('a harness-style git commit -m gets exactly one trailer after a blank line', async () => {
  const t = setup('co-basic')
  try {
    assert.equal(await configureCoAuthorHook(t.git, TRAILER), true)
    t.change()
    t.sh(['commit', '-q', '-am', 'Fix the thing'])
    assert.equal(t.body(), `Fix the thing\n\n${TRAILER}\n`)
    assert.equal(t.count(), 1)
    // Multi-line message with its own body and trailer keeps them.
    t.change()
    t.sh(['commit', '-q', '-am', 'Subject\n\nBody text\n\nSigned-off-by: Ada <ada@x>'])
    const body = t.body()
    assert.match(body, /^Subject\n\nBody text\n\nSigned-off-by: Ada <ada@x>\nCo-Authored-By: flow-app\[bot\]/)
    assert.equal(t.count(), 1)
  } finally {
    t.tmp.cleanup()
  }
})

test('an existing trailer in any case is not added twice', async () => {
  const t = setup('co-case')
  try {
    await configureCoAuthorHook(t.git, TRAILER)
    t.change()
    t.sh(['commit', '-q', '-am', 'One\n\nco-authored-by: flow-app[bot] <42+flow-app[bot]@users.noreply.github.com>'])
    assert.equal(t.count(), 1)
    t.change()
    t.sh(['commit', '-q', '-am', `Two\n\n${TRAILER}`])
    assert.equal(t.count(), 1)
  } finally {
    t.tmp.cleanup()
  }
})

test('amend, fixup, squash and merge commits never duplicate the trailer', async () => {
  const t = setup('co-rewrite')
  try {
    await configureCoAuthorHook(t.git, TRAILER)
    t.change()
    t.sh(['commit', '-q', '-am', 'Feature'])
    t.sh(['commit', '-q', '--amend', '--no-edit'])
    assert.equal(t.count(), 1)
    t.sh(['commit', '-q', '--amend', '-m', 'Feature reworded'])
    assert.equal(t.count(), 1)
    t.change()
    t.sh(['commit', '-q', '-a', '--fixup', 'HEAD'])
    assert.equal(t.count(), 1)
    t.change()
    t.sh(['commit', '-q', '-a', '--squash', 'HEAD~1', '-m', 'extra'])
    assert.equal(t.count(), 1)
    // A merge commit gets no trailer of its own; it contains the branch's.
    t.sh(['checkout', '-q', '-b', 'side'])
    writeFileSync(join(t.repoDir, 'b.txt'), 'b\n')
    t.sh(['add', '.'])
    t.sh(['commit', '-q', '-m', 'Side work'])
    t.sh(['checkout', '-q', 'main'])
    writeFileSync(join(t.repoDir, 'c.txt'), 'c\n')
    t.sh(['add', '.'])
    t.sh(['commit', '-q', '-m', 'Main work'])
    t.sh(['merge', '-q', '--no-ff', '-m', 'Merge side', 'side'])
    assert.equal(t.body(), 'Merge side\n')
    // A squash merge's message holds the squashed commits' trailers, once each.
    t.sh(['checkout', '-q', '-b', 'sq', 'HEAD~2'])
    t.sh(['merge', '-q', '--squash', 'side'])
    t.sh(['commit', '-q', '--no-edit'])
    assert.equal(t.count(), 1)
  } finally {
    t.tmp.cleanup()
  }
})

test('a husky-style core.hooksPath keeps running, and its failure blocks the commit', async () => {
  const t = setup('co-husky')
  try {
    t.hook(join(t.repoDir, '.husky', 'pre-commit'), 'echo ran >> "$(git rev-parse --show-toplevel)/pre-commit.log"; [ ! -f "$(git rev-parse --show-toplevel)/block" ]')
    t.hook(join(t.repoDir, '.husky', 'commit-msg'), 'grep -q "^Fix" "$1" || { echo "bad subject" >&2; exit 7; }')
    t.hook(join(t.repoDir, '.husky', 'prepare-commit-msg'), 'echo "Ticket: ENG-1" >> "$1"')
    t.sh(['config', 'core.hooksPath', '.husky'])
    await configureCoAuthorHook(t.git, TRAILER)
    assert.equal(t.sh(['config', '--local', '--get', 'core.hooksPath']).stdout.trim(), join(t.repoDir, '.git', FLOW_HOOKS_DIR))

    t.change()
    t.sh(['commit', '-q', '-am', 'Fix it'])
    assert.equal(readFileSync(join(t.repoDir, 'pre-commit.log'), 'utf8'), 'ran\n')
    assert.match(t.body(), /^Fix it\nTicket: ENG-1\n\nCo-Authored-By: flow-app\[bot\]/)
    assert.equal(t.count(), 1)

    // commit-msg failure (with its exit code) blocks the commit.
    t.change()
    const bad = t.sh(['commit', '-q', '-am', 'nope'], { allowFail: true })
    assert.notEqual(bad.status, 0)
    assert.match(bad.stderr, /bad subject/)
    // pre-commit failure blocks it too.
    writeFileSync(join(t.repoDir, 'block'), '')
    const blocked = t.sh(['commit', '-q', '-am', 'Fix again'], { allowFail: true })
    assert.notEqual(blocked.status, 0)
    assert.equal(t.sh(['log', '--oneline']).stdout.trim().split('\n').length, 2)
  } finally {
    t.tmp.cleanup()
  }
})

test('a failing original prepare-commit-msg stops the commit before any trailer is added', async () => {
  const t = setup('co-prep-fail')
  try {
    t.hook(join(t.repoDir, '.git', 'hooks', 'prepare-commit-msg'), 'echo "args: $# $2" >> "$(git rev-parse --git-dir)/prep.log"; exit 3')
    await configureCoAuthorHook(t.git, TRAILER)
    t.change()
    const result = t.sh(['commit', '-q', '-am', 'x'], { allowFail: true })
    assert.notEqual(result.status, 0)
    assert.equal(readFileSync(join(t.repoDir, '.git', 'prep.log'), 'utf8'), 'args: 2 message\n')
  } finally {
    t.tmp.cleanup()
  }
})

test('hooks in .git/hooks still run alongside the trailer', async () => {
  const t = setup('co-githooks')
  try {
    t.hook(join(t.repoDir, '.git', 'hooks', 'prepare-commit-msg'), 'echo "Via: git-hooks" >> "$1"')
    t.hook(join(t.repoDir, '.git', 'hooks', 'post-commit'), 'echo "post $#" >> "$(git rev-parse --git-dir)/post.log"')
    t.hook(join(t.repoDir, '.git', 'hooks', 'pre-push'), 'cat > "$(git rev-parse --git-dir)/pre-push.in"; echo "$1" > "$(git rev-parse --git-dir)/pre-push.arg"; exit 0')
    await configureCoAuthorHook(t.git, TRAILER)
    t.change()
    t.sh(['commit', '-q', '-am', 'Change'])
    assert.match(t.body(), /^Change\nVia: git-hooks\n\nCo-Authored-By: flow-app\[bot\]/)
    assert.equal(readFileSync(join(t.repoDir, '.git', 'post.log'), 'utf8'), 'post 0\n')
    // Delegates keep stdin and arguments (pre-push gets the remote and ref lines).
    const remote = join(t.tmp.path, 'remote.git')
    spawnSync('git', ['init', '-q', '--bare', remote], { env: t.env })
    t.sh(['remote', 'add', 'origin', remote])
    t.sh(['push', '-q', 'origin', 'main'])
    assert.equal(readFileSync(join(t.repoDir, '.git', 'pre-push.arg'), 'utf8').trim(), 'origin')
    assert.match(readFileSync(join(t.repoDir, '.git', 'pre-push.in'), 'utf8'), /^refs\/heads\/main [0-9a-f]{40} refs\/heads\/main /)
  } finally {
    t.tmp.cleanup()
  }
})

test('the runner turn-end commit carries one trailer and re-running the setup is idempotent', async () => {
  const t = setup('co-runner')
  try {
    t.hook(join(t.repoDir, '.git', 'hooks', 'pre-commit'), 'echo ran >> "$(git rev-parse --git-dir)/pc.log"')
    await configureCoAuthorHook(t.git, TRAILER)
    await configureCoAuthorHook(t.git, TRAILER)
    assert.equal(t.sh(['config', '--local', '--get', 'flow.originalhookspath']).stdout.trim(), join(t.repoDir, '.git', 'hooks'))
    t.change()
    const sha = await t.git.commitAll(commitMessage('Add feature\n\nlong', TRAILER), { authorName: 'Ada', authorEmail: 'ada@x', committerName: 'bot', committerEmail: 'bot@x' })
    assert.match(sha, /^[0-9a-f]{40}$/)
    assert.equal(t.body(sha), `Add feature\n\n${TRAILER}\n`)
    assert.equal(t.count(sha), 1)
    // A harness commit afterwards still works and chains pre-commit.
    t.change()
    t.sh(['commit', '-q', '-am', 'More'])
    assert.equal(t.count(), 1)
    assert.equal(readFileSync(join(t.repoDir, '.git', 'pc.log'), 'utf8'), 'ran\n')
  } finally {
    t.tmp.cleanup()
  }
})

test('without a trailer nothing is installed, and a previous install is undone', async () => {
  const t = setup('co-none')
  try {
    assert.equal(await configureCoAuthorHook(t.git, undefined), false)
    assert.equal(t.sh(['config', '--local', '--get', 'core.hooksPath'], { allowFail: true }).status, 1)
    assert.equal(existsSync(join(t.repoDir, '.git', FLOW_HOOKS_DIR)), false)
    t.change()
    t.sh(['commit', '-q', '-am', 'Plain'])
    assert.equal(t.body(), 'Plain\n')

    // Installed first (over a custom hooksPath), then removed: the original path is back.
    t.hook(join(t.repoDir, '.husky', 'pre-commit'), 'exit 0')
    t.sh(['config', 'core.hooksPath', '.husky'])
    await configureCoAuthorHook(t.git, TRAILER)
    assert.equal(await configureCoAuthorHook(t.git, undefined), false)
    assert.equal(t.sh(['config', '--local', '--get', 'core.hooksPath']).stdout.trim(), join(t.repoDir, '.husky'))
    assert.equal(t.sh(['config', '--local', '--get', 'flow.originalhookspath'], { allowFail: true }).status, 1)
    assert.equal(existsSync(join(t.repoDir, '.git', FLOW_HOOKS_DIR)), false)
    t.change()
    t.sh(['commit', '-q', '-am', 'Plain again'])
    assert.equal(t.body(), 'Plain again\n')
    // And with the default hooks directory the setting disappears entirely.
    const u = setup('co-none2')
    try {
      await configureCoAuthorHook(u.git, TRAILER)
      await configureCoAuthorHook(u.git, '')
      assert.equal(u.sh(['config', '--local', '--get', 'core.hooksPath'], { allowFail: true }).status, 1)
    } finally {
      u.tmp.cleanup()
    }
  } finally {
    t.tmp.cleanup()
  }
})

test('a restored workspace whose repo changed core.hooksPath is wrapped again', async () => {
  const t = setup('co-restore')
  try {
    await configureCoAuthorHook(t.git, TRAILER)
    // The project later runs `husky install`, replacing the setting.
    t.hook(join(t.repoDir, '.husky', 'pre-commit'), 'echo husky >> "$(git rev-parse --git-dir)/h.log"')
    t.sh(['config', '--local', 'core.hooksPath', '.husky'])
    await configureCoAuthorHook(t.git, TRAILER)
    t.change()
    t.sh(['commit', '-q', '-am', 'After restore'])
    assert.equal(t.count(), 1)
    assert.equal(readFileSync(join(t.repoDir, '.git', 'h.log'), 'utf8'), 'husky\n')
  } finally {
    t.tmp.cleanup()
  }
})
