import assert from 'node:assert/strict'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { test } from 'node:test'
import { BranchNotFoundError, classifyRemoteFailure, Git, RemoteQueryError, type Runner } from '../src/git/git.js'
import type { BootstrapRepo } from '../src/protocol.js'
import { sh, tempDir } from './helpers.js'

// Continuing a coding session: the runner checks out the session branch in a
// fresh clone (workspace lost) or a preserved workspace. A branch that never
// reached origin is created from the exact baseline, a preserved workspace's
// commits and uncommitted changes are never dropped, a failed query of origin
// is never read as "no such branch", and origin's branch is never overwritten.

const ID = ['-c', 'user.name=s', '-c', 'user.email=s@x']

interface Fixture {
  root: string
  origin: string
  env: NodeJS.ProcessEnv
  baseline: string
  repo: BootstrapRepo
  clone: (name: string) => Promise<Git>
  in: (dir: string, args: string[]) => string
  cleanup: () => void
}

function fixture(): Fixture {
  const tmp = tempDir('resume')
  const origin = join(tmp.path, 'origin.git')
  const seed = join(tmp.path, 'seed')
  const env = { PATH: '/usr/bin:/bin', HOME: tmp.path, GIT_CONFIG_NOSYSTEM: '1' }
  sh('git', ['init', '-q', '--bare', '-b', 'main', origin], { env })
  sh('git', ['init', '-q', '-b', 'main', seed], { env })
  writeFileSync(join(seed, 'README.md'), '# app\n')
  sh('git', [...ID, 'add', '.'], { cwd: seed, env })
  sh('git', [...ID, 'commit', '-q', '-m', 'init'], { cwd: seed, env })
  sh('git', ['push', '-q', origin, 'main'], { cwd: seed, env })
  const baseline = sh('git', ['rev-parse', 'HEAD'], { cwd: seed, env }).trim()
  // main moves on after the session's first clone.
  writeFileSync(join(seed, 'later.txt'), 'later\n')
  sh('git', [...ID, 'add', '.'], { cwd: seed, env })
  sh('git', [...ID, 'commit', '-q', '-m', 'later'], { cwd: seed, env })
  sh('git', ['push', '-q', origin, 'main'], { cwd: seed, env })
  mkdirSync(join(tmp.path, 'workspace'))
  const repo: BootstrapRepo = { codeHost: 'github', hostName: 'github.com', fullName: 'acme/app', cloneUrl: origin, baseRef: 'main', branchName: 'flow/x', existingBranch: false }
  return {
    root: tmp.path,
    origin,
    env,
    baseline,
    repo,
    clone: async (name) => {
      const git = new Git({ repoDir: join(tmp.path, 'workspace', name), env, credentialHelper: 'h', signProgram: 's' })
      await git.clone(origin)
      await git.configureCredentials()
      await git.configureIdentity({ authorName: 'Ada', authorEmail: 'ada@x', committerName: 'bot', committerEmail: 'bot@x' })
      return git
    },
    in: (dir, args) => sh('git', args, { cwd: dir, env }).trim(),
    cleanup: tmp.cleanup,
  }
}

function remoteHas(f: Fixture, branch: string): boolean {
  try {
    sh('git', ['rev-parse', '--verify', '--quiet', `refs/heads/${branch}`], { cwd: f.origin, env: f.env })
    return true
  } catch {
    return false
  }
}

const identity = { authorName: 'Ada', authorEmail: 'ada@x', committerName: 'bot', committerEmail: 'bot@x' }

test('resume: stopped right after the clone, workspace lost: the branch is created again from the exact baseline', async () => {
  const f = fixture()
  try {
    const git = await f.clone('repo')
    const result = await git.checkoutSessionBranch({ ...f.repo, baseSha: f.baseline })
    assert.deepEqual(result, { source: 'created', base: f.baseline, note: undefined })
    assert.equal(await git.head(), f.baseline, 'not the moved main')
    assert.equal(f.in(git.repoDir, ['symbolic-ref', '--short', 'HEAD']), 'flow/x')
    assert.equal(remoteHas(f, 'flow/x'), false, 'checking out never publishes a branch')
    // Repeated continue (another fresh sandbox): the same result.
    const again = await f.clone('repo2')
    await again.checkoutSessionBranch({ ...f.repo, baseSha: f.baseline })
    assert.equal(await again.head(), f.baseline)
    // Without a recorded baseline (a new session) the base ref is used.
    const fresh = await f.clone('repo3')
    assert.equal((await fresh.checkoutSessionBranch(f.repo)).base, 'origin/main')
  } finally {
    f.cleanup()
  }
})

test('resume: a baseline origin does not have falls back to the base ref and says so', async () => {
  const f = fixture()
  try {
    const git = await f.clone('repo')
    const result = await git.checkoutSessionBranch({ ...f.repo, baseSha: 'deadbeef'.repeat(5) })
    assert.equal(result.source, 'created')
    assert.equal(result.base, 'origin/main')
    assert.match(result.note ?? '', /baseline deadbeefdead is not on origin/)
  } finally {
    f.cleanup()
  }
})

test('resume: a preserved workspace keeps its unpushed commits and uncommitted changes', async () => {
  const f = fixture()
  try {
    const git = await f.clone('repo')
    await git.checkoutSessionBranch({ ...f.repo, baseSha: f.baseline })
    writeFileSync(join(git.repoDir, 'work.txt'), 'committed\n')
    const committed = await git.commitAll('Work\n', identity)
    writeFileSync(join(git.repoDir, 'dirty.txt'), 'uncommitted\n')
    writeFileSync(join(git.repoDir, 'README.md'), '# app (edited)\n')
    // The next turn prepares the same (restored) workspace again, with and without the server's expectation.
    for (const existingBranch of [false, true]) {
      const result = await git.checkoutSessionBranch({ ...f.repo, baseSha: f.baseline, existingBranch })
      assert.equal(result.source, 'kept')
      assert.equal(await git.head(), committed, 'the unpushed commit stays on the branch')
      assert.equal(readFileSync(join(git.repoDir, 'dirty.txt'), 'utf8'), 'uncommitted\n')
      assert.equal(readFileSync(join(git.repoDir, 'README.md'), 'utf8'), '# app (edited)\n')
    }
    assert.equal(remoteHas(f, 'flow/x'), false)
    // The next push publishes the kept work.
    await git.commitAll('More\n', identity)
    await git.push('flow/x')
    assert.equal(f.in(f.origin, ['rev-parse', 'refs/heads/flow/x']), await git.head())
  } finally {
    f.cleanup()
  }
})

test('resume: committed but unpushed, then origin gained the branch elsewhere: the workspace copy is kept, origin is not overwritten', async () => {
  const f = fixture()
  try {
    const git = await f.clone('repo')
    await git.checkoutSessionBranch(f.repo)
    writeFileSync(join(git.repoDir, 'mine.txt'), 'mine\n')
    const mine = await git.commitAll('Mine\n', identity)
    // Concurrent recovery published the branch from another sandbox.
    const other = await f.clone('other')
    await other.checkoutSessionBranch(f.repo)
    writeFileSync(join(other.repoDir, 'theirs.txt'), 'theirs\n')
    const theirs = await other.commitAll('Theirs\n', identity)
    await other.push('flow/x')
    const result = await git.checkoutSessionBranch({ ...f.repo, existingBranch: true })
    assert.equal(result.source, 'kept')
    assert.equal(await git.head(), mine)
    await assert.rejects(git.push('flow/x'), 'a diverged push is rejected, never forced')
    assert.equal(f.in(f.origin, ['rev-parse', 'refs/heads/flow/x']), theirs)
  } finally {
    f.cleanup()
  }
})

test('resume: pushed (with or without a PR): a fresh clone checks out origin, a behind workspace fast-forwards with its changes', async () => {
  const f = fixture()
  try {
    const first = await f.clone('repo')
    await first.checkoutSessionBranch(f.repo)
    writeFileSync(join(first.repoDir, 'a.txt'), 'a\n')
    const pushed = await first.commitAll('A\n', identity)
    await first.push('flow/x')
    // Workspace lost: a fresh clone.
    const fresh = await f.clone('repo2')
    assert.equal((await fresh.checkoutSessionBranch({ ...f.repo, existingBranch: true })).source, 'remote')
    assert.equal(await fresh.head(), pushed)
    assert.equal(f.in(fresh.repoDir, ['rev-parse', '--abbrev-ref', '@{upstream}']), 'origin/flow/x')
    // Even if the server did not know it was pushed, origin's branch is used, never recreated from base.
    const unaware = await f.clone('repo3')
    assert.equal((await unaware.checkoutSessionBranch({ ...f.repo, baseSha: f.baseline })).source, 'remote')
    assert.equal(await unaware.head(), pushed)
    // A preserved workspace behind origin (someone pushed more) fast-forwards and keeps its uncommitted change.
    writeFileSync(join(fresh.repoDir, 'b.txt'), 'b\n')
    const ahead = await fresh.commitAll('B\n', identity)
    await fresh.push('flow/x')
    writeFileSync(join(first.repoDir, 'dirty.txt'), 'dirty\n')
    assert.equal((await first.checkoutSessionBranch({ ...f.repo, existingBranch: true })).source, 'remote')
    assert.equal(await first.head(), ahead)
    assert.equal(readFileSync(join(first.repoDir, 'dirty.txt'), 'utf8'), 'dirty\n')
  } finally {
    f.cleanup()
  }
})

test('resume: the remote branch was deleted: a lost workspace fails as not found, a preserved one keeps its copy', async () => {
  const f = fixture()
  try {
    const kept = await f.clone('repo')
    await kept.checkoutSessionBranch(f.repo)
    writeFileSync(join(kept.repoDir, 'a.txt'), 'a\n')
    const sha = await kept.commitAll('A\n', identity)
    await kept.push('flow/x')
    f.in(f.origin, ['update-ref', '-d', 'refs/heads/flow/x'])
    const fresh = await f.clone('repo2')
    await assert.rejects(fresh.checkoutSessionBranch({ ...f.repo, existingBranch: true }), (error: unknown) => error instanceof BranchNotFoundError && /flow\/x/.test(error.message))
    assert.notEqual(f.in(fresh.repoDir, ['symbolic-ref', '--short', 'HEAD']), 'flow/x', 'no branch was invented from base')
    const result = await kept.checkoutSessionBranch({ ...f.repo, existingBranch: true })
    assert.equal(result.source, 'kept')
    assert.equal(await kept.head(), sha)
    assert.equal(remoteHas(f, 'flow/x'), false, 'nothing is re-created on origin by the checkout')
  } finally {
    f.cleanup()
  }
})

test('resume: a failed remote query is an error of its kind, never "branch not found"', async () => {
  const f = fixture()
  try {
    const git = await f.clone('repo')
    // origin unreachable (a real git failure)
    f.in(git.repoDir, ['remote', 'set-url', 'origin', join(f.root, 'missing.git')])
    await assert.rejects(git.checkoutSessionBranch({ ...f.repo, baseSha: f.baseline }), (error: unknown) => error instanceof RemoteQueryError && !(error instanceof BranchNotFoundError))
    assert.equal(f.in(git.repoDir, ['symbolic-ref', '--short', 'HEAD']), 'main', 'nothing was checked out')

    // Simulated host answers for ls-remote.
    const cases: Array<[string, number, boolean, string]> = [
      ['fatal: Authentication failed for https://github.com/acme/app.git/', 128, false, 'auth'],
      ['remote: Repository not found.\nfatal: repository not found', 128, false, 'auth'],
      ['fatal: unable to access: Could not resolve host: github.com', 128, false, 'network'],
      ['', 124, true, 'network'],
      ['fatal: unable to access: The requested URL returned error: 503', 128, false, 'unavailable'],
      ['error: RPC failed; HTTP 502 curl 22', 128, false, 'unavailable'],
      ['fatal: something odd', 128, false, 'unknown'],
    ]
    for (const [stderr, code, timedOut, kind] of cases) {
      const calls: string[][] = []
      const run: Runner = async (_command, args) => {
        calls.push(args)
        if (args.includes('ls-remote')) return { code, stdout: '', stderr, timedOut }
        return { code: 0, stdout: '', stderr: '', timedOut: false }
      }
      const fake = new Git({ repoDir: git.repoDir, env: f.env, credentialHelper: 'h', signProgram: 's', run })
      await assert.rejects(fake.checkoutSessionBranch({ ...f.repo, existingBranch: false }), (error: unknown) => error instanceof RemoteQueryError && error.kind === kind)
      assert.ok(!calls.some((args) => args.includes('checkout')), `no checkout after a ${kind} failure`)
      assert.equal(classifyRemoteFailure(stderr, timedOut), kind)
    }
    // Exit 2 from ls-remote --exit-code is the host's "no such branch".
    const notFound: Runner = async (_command, args) => ({ code: args.includes('ls-remote') ? 2 : 1, stdout: '', stderr: '', timedOut: false })
    const fake = new Git({ repoDir: git.repoDir, env: f.env, credentialHelper: 'h', signProgram: 's', run: notFound })
    await assert.rejects(fake.checkoutSessionBranch({ ...f.repo, existingBranch: true }), BranchNotFoundError)
    assert.equal(existsSync(join(git.repoDir, '.git')), true)
  } finally {
    f.cleanup()
  }
})
