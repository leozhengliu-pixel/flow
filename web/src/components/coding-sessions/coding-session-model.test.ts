import { describe, expect, it } from 'vitest'
import type { CodeReview } from '@/types/flow'
import type { CodingEnvironment, CodingErrorCode, SessionPullRequest } from '@/types/coding-sessions'
import {
  CODING_ERROR_CODES,
  CODING_STATE_LABELS,
  ciStatus,
  codingErrorInfo,
  codingModelLabel,
  codingSessionCapabilities,
  codingSessionIdFromToolResult,
  codingSessionIdsFromMessage,
  currentSandbox,
  formatCodingCost,
  normalizeRepository,
  pullRequestDisplayState,
  pullRequestNumberLabel,
  reviewForPullRequest,
  suggestEnvironment,
} from './coding-session-model'
import { codingSessionUiZhCN } from '@/i18n/translations-coding-session-ui'
import { makeSession } from './coding-session-test-fixtures'

const env = (id: string, repository: string, extra: Partial<CodingEnvironment> = {}): CodingEnvironment => ({ id, name: id, codeHost: 'github', hostName: 'github.com', repository, mise: {}, version: 1, createdAt: '', updatedAt: '', ...extra })

describe('coding session model', () => {
  it('uses Linear labels for every state', () => {
    expect(CODING_STATE_LABELS).toEqual({ pending: 'Starting', active: 'Working', stopping: 'Stopping…', complete: 'Finished', awaitingInput: 'Needs input', error: 'Errored', stale: 'Stopped' })
  })

  it('maps every error code to translated copy and a next action', () => {
    const expected: Partial<Record<CodingErrorCode, string[]>> = {
      noCodeAccess: ['link', 'restart'], noGitHubConnection: ['link'], noGitLabConnection: ['link'],
      requiresGithubConnection: ['link', 'restart'], missingCommitSigningKey: ['link', 'restart'], noPolicyRepos: ['link'],
      quotaExceeded: ['link'], personalAccessDenied: [], permissionDenied: [], modelUnavailable: ['restartWithDefaultModel'],
      environmentSetupFailed: ['fixWithAgent', 'viewLogs'], snapshotFailed: ['fixWithAgent', 'viewLogs'],
      pushFailed: ['createPullRequest', 'restart'], pullRequestFailed: ['createPullRequest', 'restart'], runnerLost: ['restart'], internal: ['restart'],
    }
    expect(CODING_ERROR_CODES).toHaveLength(31)
    for (const code of CODING_ERROR_CODES) {
      const info = codingErrorInfo(code, { workspace: 'acme' })
      expect(info.title, code).not.toBe('')
      expect(codingSessionUiZhCN[info.title], `zh title ${code}`).toBeTruthy()
      expect(codingSessionUiZhCN[info.message], `zh message ${code}`).toBeTruthy()
      for (const action of info.actions) if (action.kind === 'link') expect(codingSessionUiZhCN[action.label], action.label).toBeTruthy()
      if (expected[code]) expect(info.actions.map(action => action.kind), code).toEqual(expected[code])
    }
    expect(codingErrorInfo('noGitHubConnection', { workspace: 'acme' }).actions[0]).toEqual({ kind: 'link', label: 'Connect GitHub', href: '/acme/settings/integrations/github' })
    expect(codingErrorInfo('missingCommitSigningKey', { workspace: 'acme' }).actions[0]).toMatchObject({ href: '/acme/settings/account/security/commit-signing-key' })
    expect(codingErrorInfo('requiresGithubConnection', { workspace: 'acme' }).actions[0]).toMatchObject({ label: 'Link GitHub account', href: '/acme/settings/integrations/github' })
    expect(codingErrorInfo('requiresGitlabConnection', { workspace: 'acme' }).actions[0]).toMatchObject({ label: 'Link GitLab account', href: '/acme/settings/integrations/gitlab' })
    expect(codingErrorInfo('gitlabTransientError', { workspace: 'acme' }).title).toBe('GitLab didn’t respond')
    expect(codingErrorInfo('quotaExceeded', { workspace: 'acme' }).actions[0]).toMatchObject({ href: '/acme/settings/usage/spend-limits' })
    expect(codingErrorInfo('noCodeAccess', { workspace: 'acme', codeHost: 'gitlab' }).actions[0]).toMatchObject({ href: '/acme/settings/integrations/gitlab' })
    expect(codingErrorInfo(undefined, { workspace: 'acme' }).title).toBe('Something went wrong')
  })

  it('offers actions by state', () => {
    expect(codingSessionCapabilities(makeSession())).toMatchObject({ stop: true, prompt: true, restart: false, approve: false, dismiss: false })
    expect(codingSessionCapabilities(makeSession({ state: 'awaitingInput', phase: 'awaitingApproval' }))).toMatchObject({ stop: false, prompt: false, approve: true })
    expect(codingSessionCapabilities(makeSession({ state: 'stopping', phase: 'stopping' }))).toMatchObject({ stop: false, prompt: false })
    expect(codingSessionCapabilities(makeSession({ state: 'error', phase: 'done', modelSelection: 'gpt-5' }))).toMatchObject({ restart: true, restartWithDefaultModel: true, dismiss: true })
    expect(codingSessionCapabilities(makeSession({ state: 'stale', phase: 'done' }))).toMatchObject({ restart: true, restartWithDefaultModel: false })
    expect(codingSessionCapabilities(makeSession({ state: 'error', phase: 'done', errorCode: 'pullRequestFailed', branchName: 'flow/x' })).createPullRequest).toBe(true)
    expect(codingSessionCapabilities(makeSession({ state: 'complete', phase: 'idle', branchName: 'flow/x', commitsByTurnActivityId: { a: [{ sha: 'abc', branch: 'flow/x', signed: false, createdAt: '' }] } })).createPullRequest).toBe(true)
  })

  it('labels the harness, model, cost and PRs', () => {
    expect(codingModelLabel(makeSession())).toEqual({ harness: 'Claude Code', model: 'claude-sonnet-4-5 (Auto)' })
    expect(codingModelLabel(makeSession({ harness: 'codex', modelSelection: 'gpt-5', model: undefined }))).toEqual({ harness: 'Codex', model: 'gpt-5' })
    expect(codingModelLabel(makeSession({ model: undefined }))).toEqual({ harness: 'Claude Code', model: 'Auto' })
    expect(formatCodingCost(0)).toBe('$0.00')
    expect(formatCodingCost(5_000)).toBe('<$0.01')
    expect(formatCodingCost(1_234_567)).toBe('$1.23')
    expect(pullRequestDisplayState({ state: 'open', draft: true })).toBe('draft')
    expect(pullRequestDisplayState({ state: 'merged', draft: true })).toBe('merged')
    expect(pullRequestNumberLabel({ codeHost: 'gitlab', number: 4 })).toBe('!4')
    expect(pullRequestNumberLabel({ codeHost: 'github', number: 4 })).toBe('#4')
  })

  it('finds the synced review and its CI status', () => {
    const review = { id: 'r1', provider: 'github', number: 7, repositoryOwner: 'Acme', repositoryName: 'App', checks: [] } as unknown as CodeReview
    const pr = { codeHost: 'github', repository: 'acme/app', number: 7 } as SessionPullRequest
    expect(reviewForPullRequest(pr, [review])).toBe(review)
    expect(reviewForPullRequest({ ...pr, reviewId: 'r1', number: 9 }, [review])).toBe(review)
    expect(ciStatus(undefined)).toBe('none')
    expect(ciStatus({ checks: [{ id: '1', name: 'a', status: 'skipped' }] })).toBe('none')
    expect(ciStatus({ checks: [{ id: '1', name: 'a', status: 'passed' }, { id: '2', name: 'b', status: 'running' }] })).toBe('pending')
    expect(ciStatus({ checks: [{ id: '1', name: 'a', status: 'failed' }, { id: '2', name: 'b', status: 'running' }] })).toBe('failed')
    expect(ciStatus({ checks: [{ id: '1', name: 'a', status: 'passed' }] })).toBe('passed')
  })

  it('normalizes repositories and picks the newest sandbox', () => {
    expect(normalizeRepository('https://github.com/Acme/App.git')).toBe('acme/app')
    expect(normalizeRepository('git@gitlab.com:group/sub/proj.git')).toBe('group/sub/proj')
    expect(normalizeRepository('Acme/App/')).toBe('acme/app')
    expect(currentSandbox({ sandboxes: [
      { id: 'old', kind: 'session', status: 'stopped', createdAt: '2026-01-01T00:00:00Z' },
      { id: 'snap', kind: 'snapshot', status: 'running', createdAt: '2026-03-01T00:00:00Z' },
      { id: 'new', kind: 'session', status: 'running', createdAt: '2026-02-01T00:00:00Z' },
    ] })?.id).toBe('new')
  })

  it('suggests an environment from the repository, else asks', () => {
    const a = env('a', 'acme/app'), b = env('b', 'acme/api'), archived = env('c', 'acme/old', { archivedAt: 'x' })
    expect(suggestEnvironment([], {})).toEqual({ kind: 'none' })
    expect(suggestEnvironment([archived], {})).toEqual({ kind: 'none' })
    expect(suggestEnvironment([a, archived], {})).toMatchObject({ kind: 'single', environment: a, reason: 'only' })
    const review = { provider: 'github', repositoryOwner: 'acme', repositoryName: 'API', issueIds: ['issue-1'] } as unknown as CodeReview
    expect(suggestEnvironment([a, b], { issue: { id: 'issue-1' }, reviews: [review] })).toMatchObject({ kind: 'single', environment: b, reason: 'linkedPullRequest' })
    expect(suggestEnvironment([a, b], { issue: { id: 'issue-1' }, lastUsedId: 'a' })).toMatchObject({ kind: 'single', environment: a, reason: 'lastUsed' })
    const ambiguous = suggestEnvironment([b, a], { issue: { id: 'issue-1' } })
    expect(ambiguous).toMatchObject({ kind: 'ambiguous' })
    expect(ambiguous.kind === 'ambiguous' && ambiguous.environments.map(item => item.id)).toEqual(['a', 'b'])
    const both = suggestEnvironment([a, b, env('d', 'acme/web')], { issue: { id: 'issue-1' }, reviews: [review, { ...review, repositoryName: 'app' } as CodeReview] })
    expect(both).toMatchObject({ kind: 'ambiguous' })
    expect(both.kind === 'ambiguous' && both.environments.map(item => item.id).slice(-1)).toEqual(['d'])
  })

  it('reads session ids from start_coding_session tool results', () => {
    expect(codingSessionIdFromToolResult({ id: 'cs', agentSessionId: 't' })).toBe('cs')
    expect(codingSessionIdFromToolResult('{"session":{"id":"cs2","agentSessionId":"t"}}')).toBe('cs2')
    expect(codingSessionIdFromToolResult({ codingSessionId: 'cs3' })).toBe('cs3')
    expect(codingSessionIdFromToolResult({ sessionId: 'cs4' })).toBe('cs4')
    expect(codingSessionIdFromToolResult('not json')).toBeUndefined()
    expect(codingSessionIdFromToolResult({ id: 'no-anchor' })).toBeUndefined()
    const call = (id: string, name: string, result: unknown, status: 'completed' | 'error' = 'completed') => ({ id, type: 'toolCall' as const, toolCall: { id, name, status, result } })
    expect(codingSessionIdsFromMessage({ parts: [call('1', 'start_coding_session', { sessionId: 'a' }), call('2', 'search', { sessionId: 'x' }), call('3', 'start_coding_session', { sessionId: 'a' }), call('4', 'start_coding_session', { sessionId: 'b' }, 'error')] })).toEqual(['a'])
  })
})

describe('session status line', () => {
  it('reads like the reference app session card', async () => {
    const { codingStatusLine, formatCodingElapsed, codingRunStartedAt } = await import('./coding-session-model')
    const { codingSessionUiZhCN } = await import('@/i18n/translations-coding-session-ui')
    expect(codingStatusLine({ state: 'active', phase: 'running' })).toEqual({ tone: 'working', label: 'Thinking…', live: true })
    expect(codingStatusLine({ state: 'pending', phase: 'provisioning' })).toEqual({ tone: 'working', label: 'Preparing sandbox', live: true, ellipsis: true })
    expect(codingStatusLine({ state: 'error', phase: 'done' }, 'Spend limit reached')).toMatchObject({ tone: 'error', label: 'Error', detail: 'Spend limit reached', live: false })
    expect(codingStatusLine({ state: 'complete', phase: 'idle', summary: 'Done' })).toMatchObject({ tone: 'done', label: 'Finished', detail: 'Done' })
    expect(codingStatusLine({ state: 'awaitingInput', phase: 'awaitingApproval' }).label).toBe('Waiting for approval')
    expect(codingStatusLine({ state: 'stale', phase: 'done' }).tone).toBe('stopped')
    for (const label of ['Thinking…', 'Error', 'Finished', 'Stopped', 'Needs input', 'Waiting for approval', 'Stopping…']) expect(codingSessionUiZhCN[label], label).toBeTruthy()
    expect(formatCodingElapsed(162_000)).toBe('02:42')
    expect(formatCodingElapsed(3_762_000)).toBe('1:02:42')
    expect(formatCodingElapsed(-5)).toBe('00:00')
    expect(codingRunStartedAt({ createdAt: 'c', startedAt: 's', turnStartedAt: 't' })).toBe('t')
    expect(codingRunStartedAt({ createdAt: 'c' })).toBe('c')
  })
})
