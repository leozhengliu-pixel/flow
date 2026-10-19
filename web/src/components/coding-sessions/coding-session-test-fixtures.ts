/** Test builders for coding session UI tests. */
import type { CodingSession } from '@/types/coding-sessions'

export function makeSession(overrides: Partial<CodingSession> = {}): CodingSession {
  return { id: 'cs-1', agentSessionId: 'task-1', issueId: 'issue-1', creatorId: 'user-1', source: 'issue', state: 'active', phase: 'running', harness: 'claudeCode', modelSelection: 'auto', model: 'claude-sonnet-4-5', costMicros: 0, version: 1, createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T00:00:00Z', ...overrides }
}
