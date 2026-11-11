import { describe, expect, it } from 'vitest'
import { makeBootstrap } from '@/test/fixtures'
import type { AgentSession } from '@/types/flow'
import { agentSessionNeedsDetail, mergeAgentSession, mergeAgentSessions } from './agent-session-merge'
import { mergeWorkspaceDirectory } from './issue-detail-cache'
import { toNavigationSnapshot } from './navigation-cache'
import { mergeWorkspaceMetadata } from './workspace-metadata-refresh'

const at = (seconds: number) => new Date(Date.UTC(2026, 10, 1, 0, 0, seconds)).toISOString()
const whole = (overrides: Partial<AgentSession> = {}): AgentSession => ({
  id: 'chat', slugId: 'chat', userId: 'viewer', title: 'Release plan', favorite: false, location: 'page', issueIds: [], skillIds: [],
  messages: [
    { id: 'q', role: 'user', content: 'Plan the release', createdAt: at(0) },
    { id: 'a', role: 'assistant', content: 'Here is the plan.', createdAt: at(1) },
  ],
  feedback: [{ comment: 'private note', updatedAt: at(1) }],
  createdAt: at(0), updatedAt: at(1), ...overrides,
})
const summaryOf = (session: AgentSession, overrides: Partial<AgentSession> = {}): AgentSession => ({ ...session, messages: [], messagesOmitted: true, ...overrides })

describe('mergeAgentSession', () => {
  it('never lets a summary of the same version empty a chat read whole, and takes its other fields', () => {
    const merged = mergeAgentSession(whole(), summaryOf(whole(), { title: 'Renamed', lastReadAt: at(2) }))
    expect(merged.messages).toHaveLength(2)
    expect(merged).toMatchObject({ title: 'Renamed', lastReadAt: at(2), messagesOmitted: false, needsDetailRefresh: false })
    expect(agentSessionNeedsDetail(merged)).toBe(false)
  })

  it('keeps the messages of a chat a newer summary outdated, marked to be read again', () => {
    const merged = mergeAgentSession(whole(), summaryOf(whole(), { updatedAt: at(9) }))
    expect(merged.messages).toHaveLength(2)
    expect(merged.needsDetailRefresh).toBe(true)
    expect(agentSessionNeedsDetail(merged)).toBe(true)
    // The read that follows replaces it and clears the mark.
    const read = whole({ updatedAt: at(9), messages: [...whole().messages, { id: 'q2', role: 'user', content: 'And the date?', createdAt: at(8) }] })
    const fresh = mergeAgentSession(merged, read)
    expect(fresh.messages).toHaveLength(3)
    expect(agentSessionNeedsDetail(fresh)).toBe(false)
  })

  it('ignores a late, older response: a summary or a whole read', () => {
    const shown = whole({ updatedAt: at(9), title: 'Current' })
    expect(mergeAgentSession(shown, summaryOf(whole(), { updatedAt: at(1), title: 'Old' }))).toBe(shown)
    expect(mergeAgentSession(shown, whole({ updatedAt: at(1), title: 'Old' }))).toBe(shown)
    // A late whole read still fills in a summary's messages, but the chat is read again.
    const summary = summaryOf(whole(), { updatedAt: at(9) })
    const filled = mergeAgentSession(summary, whole({ updatedAt: at(1) }))
    expect(filled.messages).toHaveLength(2)
    expect(filled.needsDetailRefresh).toBe(true)
  })

  it('takes a newer whole copy (an edit, a new reply) as it is', () => {
    const edited = whole({ updatedAt: at(9), messages: [{ id: 'q', role: 'user', content: 'Plan the launch', createdAt: at(0) }] })
    expect(mergeAgentSession(whole(), edited)).toBe(edited)
  })

  it('drops chats a fresh list no longer has (deleted, or no longer the viewer’s) with their messages', () => {
    const other = whole({ id: 'other', slugId: 'other' })
    expect(mergeAgentSessions([whole(), other], [summaryOf(other)]).map(item => item.id)).toEqual(['other'])
    expect(mergeAgentSessions([whole()], [])).toEqual([])
  })
})

describe('workspace refreshes keep chats read whole', () => {
  it('a metadata refresh after the chat was read keeps its messages', () => {
    const current = makeBootstrap({ agentSessions: [whole()] })
    const next = makeBootstrap({ agentSessions: [summaryOf(whole())] })
    expect(mergeWorkspaceMetadata(current, next).agentSessions[0].messages).toHaveLength(2)
  })

  it('a paged bootstrap keeps them for the same viewer and workspace only', () => {
    const current = makeBootstrap({ issueCollectionPaged: true, agentSessions: [whole()] })
    const same = makeBootstrap({ issueCollectionPaged: true, agentSessions: [summaryOf(whole())] })
    expect(mergeWorkspaceDirectory(current, same).agentSessions[0].messages).toHaveLength(2)
    const otherViewer = makeBootstrap({ issueCollectionPaged: true, agentSessions: [summaryOf(whole())], viewer: { ...current.viewer, id: 'someone-else' } })
    expect(mergeWorkspaceDirectory(current, otherViewer).agentSessions[0].messages).toEqual([])
    const otherWorkspace = makeBootstrap({ issueCollectionPaged: true, agentSessions: [summaryOf(whole())], workspace: { ...current.workspace, id: 'another-workspace' } })
    expect(mergeWorkspaceDirectory(current, otherWorkspace).agentSessions[0].messages).toEqual([])
  })

  it('the persisted navigation cache holds marked summaries without messages or notes', () => {
    const snapshot = toNavigationSnapshot(makeBootstrap({ agentSessions: [whole({ needsDetailRefresh: true })] }))
    const [cached] = snapshot.agentSessions
    expect(cached).toMatchObject({ id: 'chat', title: 'Release plan', messages: [], messagesOmitted: true })
    expect(cached).not.toHaveProperty('feedback')
    expect(cached).not.toHaveProperty('needsDetailRefresh')
    expect(JSON.stringify(snapshot)).not.toContain('Here is the plan.')
  })
})
