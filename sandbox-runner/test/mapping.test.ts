import assert from 'node:assert/strict'
import { existsSync, readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { test } from 'node:test'
import { harnessFor } from '../src/harness/index.js'
import { classifyError, parseJsonl, tail, type StreamMapper } from '../src/mapping/common.js'
import { unwrapShell } from '../src/mapping/codex.js'

// Recorded-fixture tests: every test/fixtures/<harness>-*.jsonl stream is
// mapped to RunnerEvents and compared with <name>.expected.json.
// *-recorded-* fixtures were captured from the pinned CLIs (claude-code
// 2.1.289, codex 0.160.0, opencode 1.18.34) running against a scripted
// fake of Flow's LLM proxy; the others follow the CLIs' documented formats.
// Refresh after a CLI upgrade with UPDATE_FIXTURES=1 npm test, then review the diff.

const dir = join(import.meta.dirname, 'fixtures')
const harnessOf: Record<string, string> = { claude: 'claudeCode', codex: 'codex', opencode: 'openSource' }

function mapFixture(file: string) {
  const prefix = file.split('-')[0]
  const adapter = harnessFor(harnessOf[prefix])
  let n = 0
  const mapper: StreamMapper = adapter.createMapper({
    turnId: 'turn_1',
    cwd: '/workspace/repo',
    model: prefix === 'claude' ? 'claude-sonnet-4-5' : prefix === 'codex' ? 'gpt-5-codex' : 'fixture-model',
    newId: () => `e${++n}`,
    now: () => new Date('2026-10-18T00:00:00.000Z'),
  })
  const events = []
  for (const record of parseJsonl(readFileSync(join(dir, file), 'utf8'))) events.push(...mapper.map(record))
  events.push(...mapper.finish())
  return { events, outcome: mapper.outcome() }
}

for (const file of readdirSync(dir).filter((name) => name.endsWith('.jsonl')).sort()) {
  test(`fixture ${file}`, () => {
    const actual = JSON.parse(JSON.stringify(mapFixture(file)))
    const expectedPath = join(dir, file.replace(/\.jsonl$/, '.expected.json'))
    if (process.env.UPDATE_FIXTURES || !existsSync(expectedPath)) {
      writeFileSync(expectedPath, JSON.stringify(actual, null, 2) + '\n')
      if (!process.env.UPDATE_FIXTURES) assert.fail(`wrote missing ${expectedPath}; review it and re-run`)
    }
    assert.deepEqual(actual, JSON.parse(readFileSync(expectedPath, 'utf8')))
  })
}

test('every event carries id, turnId and createdAt', () => {
  for (const file of readdirSync(dir).filter((name) => name.endsWith('.jsonl'))) {
    for (const event of mapFixture(file).events) {
      assert.ok(event.id && event.turnId === 'turn_1' && event.createdAt, `${file}: ${JSON.stringify(event)}`)
    }
  }
})

test('recorded turns: resume ids, final responses and elicitations', () => {
  const claude1 = mapFixture('claude-recorded-turn1.jsonl')
  assert.equal(claude1.outcome.completed, true)
  assert.match(claude1.outcome.harnessSessionId ?? '', /^[0-9a-f-]{36}$/)
  assert.equal(claude1.events.at(-2)?.type, 'response')
  const claude2 = mapFixture('claude-recorded-turn2.jsonl')
  assert.equal(claude2.outcome.harnessSessionId, claude1.outcome.harnessSessionId, 'resume keeps the session id')
  assert.equal(claude2.outcome.elicited, true)
  assert.ok(claude2.events.some((event) => event.type === 'elicitation' && event.body?.includes('Which greeting')))
  for (const prefix of ['codex', 'opencode']) {
    const turn1 = mapFixture(`${prefix}-recorded-turn1.jsonl`)
    assert.equal(turn1.outcome.completed, true, prefix)
    assert.equal(turn1.outcome.finalText, 'Created `hello.txt` containing a greeting.', prefix)
    const ran = turn1.events.find((event) => event.type === 'action' && !event.ephemeral && event.action?.action === 'Ran command')
    assert.equal(ran?.action?.parameter, 'echo hello > hello.txt && cat hello.txt', prefix)
    assert.equal(ran?.action?.exitCode, 0, prefix)
    const turn2 = mapFixture(`${prefix}-recorded-turn2.jsonl`)
    assert.equal(turn2.outcome.elicited, true, prefix)
    assert.ok(turn2.outcome.harnessSessionId, prefix)
  }
})

test('errors are classified for the UI', () => {
  assert.equal(mapFixture('claude-error.jsonl').outcome.error?.code, 'quotaExceeded')
  assert.equal(mapFixture('claude-max-turns.jsonl').outcome.error?.code, 'harnessFailed')
  assert.equal(mapFixture('codex-failed.jsonl').outcome.error?.code, 'modelUnavailable')
  assert.equal(mapFixture('codex-failed.jsonl').events.filter((event) => event.type === 'error').length, 1, 'error and turn.failed are deduplicated')
  assert.equal(mapFixture('opencode-error.jsonl').outcome.error?.code, 'quotaExceeded')
  assert.equal(classifyError('segfault'), 'harnessFailed')
})

test('subagent output does not surface; plans map statuses', () => {
  const { events } = mapFixture('claude-tools.jsonl')
  assert.ok(!events.some((event) => event.body?.includes('Subagent text')))
  assert.deepEqual(events.find((event) => event.type === 'plan')?.plan, [
    { content: 'Find the date helper', status: 'completed' },
    { content: 'Fix the timezone bug', status: 'inProgress' },
    { content: 'Run the tests', status: 'pending' },
  ])
  const diffs = events.filter((event) => event.type === 'diff').flatMap((event) => event.diff ?? [])
  assert.deepEqual(diffs, [
    { path: 'src/date.ts', additions: 2, deletions: 1, status: 'modified' },
    { path: 'src/date.local.test.ts', additions: 5, deletions: 0, status: 'added' },
  ])
  const failed = events.find((event) => event.action?.action === 'Ran command' && !event.ephemeral)
  assert.equal(failed?.action?.exitCode, 1)
})

test('helpers', () => {
  assert.equal(unwrapShell("bash -lc 'echo hi'"), 'echo hi')
  assert.equal(unwrapShell("/bin/bash -lc 'echo '\\''x'\\'''"), "echo 'x'")
  assert.equal(unwrapShell('ls -la'), 'ls -la')
  const long = 'é'.repeat(5000)
  const cut = tail(long, 4096)
  assert.ok(Buffer.byteLength(cut) <= 4096 + 3)
  assert.ok(cut.startsWith('…é'))
})
