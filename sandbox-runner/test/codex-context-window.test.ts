import assert from 'node:assert/strict'
import { join } from 'node:path'
import { test } from 'node:test'
import { claudeHarness } from '../src/harness/claude.js'
import { codexConfig, codexContextWindow, codexHarness } from '../src/harness/codex.js'
import { opencodeConfig, opencodeHarness } from '../src/harness/opencode.js'
import type { HarnessContext } from '../src/harness/types.js'
import type { Logger } from '../src/log.js'
import type { BootstrapHarness, BootstrapResponse } from '../src/protocol.js'

function bootstrap(harness: BootstrapHarness): BootstrapResponse {
  return {
    sandboxId: 'sbx1',
    mode: 'session',
    restored: false,
    repo: { codeHost: 'github', hostName: 'github.com', fullName: 'acme/app', cloneUrl: '', baseRef: 'main', branchName: 'flow/x', existingBranch: false },
    harness,
    llm: { anthropicBaseUrl: 'http://flow:8090/internal/llm-proxy/anthropic', openaiBaseUrl: 'http://flow:8090/internal/llm-proxy/openai/v1', openSourceBaseUrl: 'http://flow:8090/internal/llm-proxy/opensource/v1' },
    environment: { mise: {}, dependencyManagement: 'custom-only', hasSecrets: false },
    git: { authorName: 'Ada', authorEmail: 'ada@example.com', committerName: 'bot', committerEmail: 'bot@example.com' },
    browser: { enabled: false },
    limits: { turnTimeoutSeconds: 60, heartbeatSeconds: 15, setupTimeoutSeconds: 60, stopGraceSeconds: 1 },
    guidance: '',
  }
}

function context(harness: BootstrapHarness, logger?: Logger): HarnessContext {
  const home = '/home/flow'
  return {
    bootstrap: bootstrap(harness),
    token: 'fcs_tok',
    paths: { workspace: '/workspace', repo: '/workspace/repo', home, flowDir: join(home, '.flow'), binDir: '/opt/flow/bin' },
    turn: { id: 't1', prompt: 'fix it', kind: 'initial' },
    guidance: 'G',
    mcp: { command: '/opt/flow/bin/flow-runner', args: ['mcp'], env: {} },
    env: { PATH: '/usr/bin' },
    logger,
  }
}

function writtenConfig(ctx: HarnessContext, resumeId?: string): string {
  const file = codexHarness.invocation({ ...ctx, resumeId }).files.find((f) => f.path.endsWith('.codex/config.toml'))
  assert.ok(file, 'config.toml is written on every invocation')
  return file.content
}

function recordingLogger(): Logger & { warnings: string[] } {
  const warnings: string[] = []
  return { warnings, debug() {}, info() {}, error() {}, warn: (msg, fields) => void warnings.push(`${msg} ${JSON.stringify(fields)}`) }
}

test('codex: model_context_window is an integer TOML value on the first turn and on resumed turns', () => {
  const ctx = context({ name: 'codex', model: 'glm-5', contextWindow: 200000 })
  for (const toml of [writtenConfig(ctx), writtenConfig(ctx, 'thread-1')]) {
    assert.match(toml, /^model = "glm-5"$/m)
    assert.match(toml, /^model_context_window = 200000$/m)
    assert.ok(!toml.includes('model_auto_compact_token_limit'), 'Flow never sets the auto-compaction threshold')
    // A top-level key: it must come before the first table header.
    assert.ok(toml.indexOf('model_context_window') < toml.indexOf('['))
  }
  assert.deepEqual(codexHarness.invocation({ ...ctx, resumeId: 'thread-1' }).args.slice(0, 3), ['exec', 'resume', 'thread-1'])
})

test('codex: no contextWindow (old API or unset model) writes no line, as before', () => {
  for (const harness of [{ name: 'codex', model: 'gpt-5-codex' }, { name: 'codex', model: 'gpt-5-codex', contextWindow: undefined }] as BootstrapHarness[]) {
    const ctx = context(harness)
    assert.ok(!writtenConfig(ctx).includes('model_context_window'))
    assert.ok(!writtenConfig(ctx, 'thread-1').includes('model_context_window'))
  }
  // A bootstrap decoded from JSON without the field.
  const decoded = JSON.parse('{"name":"codex","model":"gpt-5"}') as BootstrapHarness
  assert.ok(!codexConfig(context(decoded)).includes('model_context_window'))
  const withField = JSON.parse('{"name":"codex","model":"glm-5","contextWindow":200000}') as BootstrapHarness
  assert.match(codexConfig(context(withField)), /^model_context_window = 200000$/m)
})

test('codex: an invalid contextWindow is ignored with a warning (defence in depth)', () => {
  for (const value of ['200000', '200k', -1, 0, 1023, 10_000_001, 200000.5, Number.NaN, Number.POSITIVE_INFINITY, 2 ** 53, true, [200000]]) {
    const logger = recordingLogger()
    const ctx = context({ name: 'codex', model: 'glm-5', contextWindow: value as unknown as number }, logger)
    assert.ok(!codexConfig(ctx).includes('model_context_window'), `value ${String(value)}`)
    assert.equal(logger.warnings.length, 1, `value ${String(value)} warns`)
    assert.match(logger.warnings[0], /ignoring invalid harness\.contextWindow/)
  }
  assert.equal(codexContextWindow(null), undefined)
  assert.equal(codexContextWindow(1024), 1024)
  assert.equal(codexContextWindow(10_000_000), 10_000_000)
})

test('other harnesses ignore contextWindow', () => {
  const claude = context({ name: 'claudeCode', model: 'glm-5', contextWindow: 200000 })
  assert.ok(!claudeHarness.invocation(claude).files.some((f) => f.content.includes('200000')))
  assert.ok(!claudeHarness.invocation(claude).args.join(' ').includes('200000'))
  const open = context({ name: 'openSource', model: 'glm-5', contextWindow: 200000 })
  assert.ok(!opencodeConfig(open).includes('200000'))
  assert.ok(!opencodeHarness.invocation(open).args.join(' ').includes('200000'))
})
