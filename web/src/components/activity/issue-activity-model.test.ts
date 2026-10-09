import { describe, expect, it } from 'vitest'
import type { ActivityEvent } from '@/types/flow'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { activityPartsText, activityTimeLabel, describeIssueActivity, describeIssueActivityParts } from './issue-activity-model'

const event = (metadata: Record<string,string>, type = 'issue.updated'): ActivityEvent => ({ id: 'event', type, actor: viewer, createdAt: '2026-09-08T06:00:00Z', metadata })

describe('issue activity projection', () => {
  it('hides description autosaves and bookkeeping without modifying stored metadata', () => {
    const metadata = { description: 'updated', descriptionBefore: 'Private old text', descriptionStateBefore: '{"type":"doc"}', documentContent: 'doc-id' }
    expect(describeIssueActivity(event(metadata))).toBeNull()
    expect(metadata.descriptionBefore).toBe('Private old text')
    expect(describeIssueActivity(event({ subscribers: 'user-1', sortOrder: '12' }))).toBeNull()
    expect(describeIssueActivity(event({}))).toBeNull()
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
    expect(describeIssueActivityParts(event({ parent: 'issue-not-held' }), context)).toEqual(['set the parent issue to ', { type: 'issue', id: 'issue-not-held', label: 'another issue' }])
    expect(describeIssueActivityParts(event({ added: 'Version 2', removed: 'Version 1' }, 'issue.releases_updated'), context)).toEqual(['added to release ', { type: 'release', id: 'release-1', label: 'Version 2' }, ', removed from release ', { type: 'release', id: 'release-0', label: 'Version 1' }])
    expect(describeIssueActivityParts(event({ recurrence: '', recurringNext: 'DEV-13', recurringNextId: 'issue-13' }), context)).toEqual(['moved the recurring schedule to ', { type: 'issue', id: 'issue-13', label: 'DEV-13' }])
    expect(activityPartsText(describeIssueActivityParts(event({ project: projectId, cycle: 'missing-cycle' }), context)!)).toBe(`added to project ${context.projects[0].name}, changed the cycle`)
  })

  it('uses compact localized timestamps', () => {
    const now = Date.parse('2026-09-08T06:42:00Z')
    expect(activityTimeLabel('2026-09-08T06:00:00Z', now, 'en-US')).toBe('42m ago')
    expect(activityTimeLabel('2026-09-08T06:00:00Z', now, 'zh-CN')).toBe('42分钟前')
    expect(activityTimeLabel('2026-09-08T06:41:50Z', now, 'en-US')).toBe('just now')
  })
})
