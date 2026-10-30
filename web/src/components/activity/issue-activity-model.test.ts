import { describe, expect, it } from 'vitest'
import type { ActivityEvent } from '@/types/flow'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { ACTIVITY_COALESCE_WINDOW_MS, activityPartsText, activityTimeLabel, canCoalesceIssueEvents, coalesceIssueEvents, describeIssueActivity, describeIssueActivityParts, issueEventFieldCount, lastChangeSeparator } from './issue-activity-model'

const event = (metadata: Record<string,string>, type = 'issue.updated'): ActivityEvent => ({ id: 'event', type, actor: viewer, createdAt: '2026-09-08T06:00:00Z', metadata })

describe('issue activity projection', () => {
  it('hides description autosaves and bookkeeping without modifying stored metadata', () => {
    const metadata = { description: 'updated', descriptionBefore: 'Private old text', descriptionStateBefore: '{"type":"doc"}', documentContent: 'doc-id' }
    expect(describeIssueActivity(event(metadata))).toBeNull()
    expect(metadata.descriptionBefore).toBe('Private old text')
    expect(describeIssueActivity(event({ subscribers: 'user-1', sortOrder: '12' }))).toBeNull()
    expect(describeIssueActivity(event({}))).toBeNull()
  })

  it('describes a closed pull request unassigning the agent and archiving its session', () => {
    const context = makeBootstrap()
    const agent = context.users.find(user => user.id !== viewer.id) ?? viewer
    expect(describeIssueActivity(event({ delegate: '', previousDelegate: agent.id, source: 'github' }), context)).toBe(`unassigned ${agent.displayName}`)
    expect(describeIssueActivity(event({ delegate: '' }), context)).toBe('removed the delegate')
    expect(describeIssueActivity(event({ sessionId: 'cs-1', agentId: agent.id }, 'issue.coding_session_archived'), context)).toBe(`Session with ${agent.displayName} archived`)
    expect(describeIssueActivity(event({ sessionId: 'cs-1' }, 'issue.coding_session_archived'), context)).toBe('Session with Flow archived')
    // A member who archived it (after removing the agent) is named by the row's actor.
    expect(describeIssueActivity(event({ sessionId: 'cs-1', agentId: agent.id, archivedBy: viewer.id }, 'issue.coding_session_archived'), context)).toBe(`archived the session with ${agent.displayName}`)
  })

  it('describes a team move without repeating a status that kept its name', () => {
    const moved = { team: 'team-2', teamName: 'Design', teamBefore: 'team-1', teamBeforeName: 'Engineering', identifier: 'DES-9', identifierBefore: 'ENG-4', stateBefore: 'In Progress', stateBeforeId: 's-1', state: 'In Progress', stateId: 's-2', cycle: '', cycleBefore: 'cycle-1' }
    expect(describeIssueActivity(event(moved))).toBe('moved the issue from Engineering to Design and removed the issue from its cycle')
    const { cycle: _cycle, cycleBefore: _cycleBefore, ...withoutCycle } = moved
    expect(describeIssueActivity(event({ ...withoutCycle, state: 'Doing' }))).toBe('moved the issue from Engineering to Design and moved from In Progress to Doing')
    expect(describeIssueActivity(event({ team: 'team-2' }))).toBe('moved the issue to another team')
  })

  it('describes recurring schedule automation', () => {
    expect(describeIssueActivity(event({ automation: 'recurring', recurringFrom: 'DEV-12', state: 'Todo' }, 'issue.created'))).toBe('created the issue from the recurring schedule of DEV-12')
    expect(describeIssueActivity(event({ recurrence: '', automation: 'recurring', recurringNext: 'DEV-13' }))).toBe('moved the recurring schedule to DEV-13')
    expect(describeIssueActivity(event({ recurrence: '' }))).toBe('stopped the recurring schedule')
    expect(describeIssueActivity(event({ recurrence: 'weekdays', nextOccurrenceAt: '2026-10-01T00:00:00Z' }))).toBe('changed the recurring schedule')
  })

  it('shows a meaningful change even when the same event also contains document snapshots', () => {
    const text = describeIssueActivity(event({ description: 'updated', descriptionBefore: 'Old description', stateBefore: 'Todo', stateBeforeId: 'internal-1', stateBeforeType: 'unstarted', state: 'In progress', stateId: 'internal-2', stateType: 'started' }))
    expect(text).toBe('moved from Todo to In progress')
    expect(text).not.toMatch(/internal|Before|description|stateId/)
  })

  it('resolves entity names and avoids exposing IDs for missing entities', () => {
    const context = makeBootstrap()
    expect(describeIssueActivity(event({ assignee: viewer.id }), context)).toBe('assigned the issue to themselves')
    expect(describeIssueActivity(event({ project: context.projects[0].id }), context)).toBe(`added to project ${context.projects[0].name}`)
    expect(describeIssueActivity(event({ labels: 'missing-id' }), context)).toBe('changed the labels')
    expect(describeIssueActivity(event({ assignee: 'deleted-user-id' }), context)).toBe('changed the assignee')
  })

  it('retains both sides of a release move and skips comment audit duplicates', () => {
    expect(describeIssueActivity(event({ added: 'Version 2', removed: 'Version 1' }, 'issue.releases_updated'))).toBe('added to release Version 2, removed from release Version 1')
    expect(describeIssueActivity(event({}, 'comment.created'))).toBeNull()
    expect(describeIssueActivity(event({}, 'issue.reacted'))).toBeNull()
  })

  it('names the resources an event references so they can render as chips', () => {
    const context = { ...makeBootstrap(), releases: [{ id: 'release-1', name: 'Version 2' }, { id: 'release-0', name: 'Version 1' }] as never }
    const projectId = context.projects[0].id
    expect(describeIssueActivityParts(event({ project: projectId }), context)).toEqual(['added to project ', { type: 'project', id: projectId, label: context.projects[0].name }])
    expect(describeIssueActivityParts(event({ labels: context.labels[0].id }), context)).toEqual(['changed labels to ', { type: 'label', id: context.labels[0].id, label: context.labels[0].name }])
    expect(describeIssueActivityParts(event({ type: 'duplicate', relatedIssueId: context.issues[0].id }, 'issue.relation_added'), context)).toEqual(['marked this as a duplicate of ', { type: 'issue', id: context.issues[0].id, label: context.issues[0].identifier }])
    expect(describeIssueActivityParts(event({ type: 'blocked_by', relatedIssueId: 'issue-not-held' }, 'issue.relation_added'), context)).toEqual(['marked this as blocked by ', { type: 'issue', id: 'issue-not-held', label: 'another issue' }])
    expect(describeIssueActivityParts(event({ type: 'related', relatedIssueId: context.issues[0].id }, 'issue.relation_added'), context)).toEqual(['added related issue ', { type: 'issue', id: context.issues[0].id, label: context.issues[0].identifier }])
    expect(describeIssueActivityParts(event({ parent: 'issue-not-held' }), context)).toEqual(['set the parent issue to ', { type: 'issue', id: 'issue-not-held', label: 'another issue' }])
    expect(describeIssueActivityParts(event({ added: 'Version 2', removed: 'Version 1' }, 'issue.releases_updated'), context)).toEqual(['added to release ', { type: 'release', id: 'release-1', label: 'Version 2' }, ', removed from release ', { type: 'release', id: 'release-0', label: 'Version 1' }])
    expect(describeIssueActivityParts(event({ recurrence: '', recurringNext: 'DEV-13', recurringNextId: 'issue-13' }), context)).toEqual(['moved the recurring schedule to ', { type: 'issue', id: 'issue-13', label: 'DEV-13' }])
    expect(activityPartsText(describeIssueActivityParts(event({ project: projectId, cycle: 'missing-cycle' }), context)!)).toBe(`added to project ${context.projects[0].name} and changed the cycle`)
  })

  it('uses compact localized timestamps', () => {
    const now = Date.parse('2026-09-08T06:42:00Z')
    expect(activityTimeLabel('2026-09-08T06:00:00Z', now, 'en-US')).toBe('42m ago')
    expect(activityTimeLabel('2026-09-08T06:00:00Z', now, 'zh-CN')).toBe('42分钟前')
    expect(activityTimeLabel('2026-09-08T06:41:50Z', now, 'en-US')).toBe('just now')
  })
})

describe('coalescing field changes', () => {
  const agent = { type: 'agent', name: 'Flow', onBehalfOfId: viewer.id }
  const change = (id: string, seconds: number, metadata: Record<string, string>, extra: Partial<ActivityEvent> = {}): ActivityEvent => ({ id, type: 'issue.updated', actor: viewer, createdAt: new Date(Date.UTC(2026, 9, 12, 9, 0, seconds)).toISOString(), metadata, botActor: agent, ...extra })

  it('folds consecutive changes of different fields by the same actor into one sentence', () => {
    const rows = coalesceIssueEvents([change('a', 0, { priority: 'Urgent' }), change('b', 3, { estimate: '3', estimateBefore: '0' }), change('c', 5, { dueDate: '2026-10-16' })])
    expect(rows).toHaveLength(1)
    expect(rows[0].id).toBe('a')
    expect(issueEventFieldCount(rows[0])).toBe(3)
    expect(describeIssueActivity(rows[0])).toBe('set priority to Urgent, set the due date to 2026-10-16 and set the estimate to 3')
  })

  it('keeps rows apart for another actor or attribution, a repeated field, a long gap or another automation', () => {
    const first = change('a', 0, { priority: 'Urgent' })
    expect(canCoalesceIssueEvents(first, change('b', 1, { estimate: '3' }, { botActor: undefined }))).toBe(false)
    expect(canCoalesceIssueEvents(first, change('b', 1, { estimate: '3' }, { actor: { ...viewer, id: 'someone' } }))).toBe(false)
    expect(canCoalesceIssueEvents(first, change('b', 1, { priority: 'Low' }))).toBe(false)
    expect(canCoalesceIssueEvents(first, change('b', 1 + ACTIVITY_COALESCE_WINDOW_MS / 1000, { estimate: '3' }))).toBe(false)
    expect(canCoalesceIssueEvents(first, change('b', 1, { state: 'Done', automation: 'pr' }))).toBe(false)
    expect(canCoalesceIssueEvents(first, { ...change('b', 1, {}), type: 'issue.relation_added' })).toBe(false)
    expect(coalesceIssueEvents([first, change('b', 1, { estimate: '3' })], () => false)).toHaveLength(2)
  })

  it('says "assigned the issue to <person>" when Flow assigned the person it works for', () => {
    const context = makeBootstrap()
    expect(describeIssueActivity(change('a', 0, { assignee: viewer.id }), context)).toBe(`assigned the issue to ${viewer.displayName}`)
  })

  it('joins the last change with the translated separator', () => {
    const zh = (text: string) => text === '{changes} and {last}' ? '{changes}，并{last}' : text
    expect(lastChangeSeparator(zh)).toBe('，并')
    expect(lastChangeSeparator(text => text)).toBe(' and ')
  })
})
