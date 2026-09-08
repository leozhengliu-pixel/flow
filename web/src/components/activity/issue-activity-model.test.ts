import { describe, expect, it } from 'vitest'
import type { ActivityEvent } from '@/types/flow'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { activityTimeLabel, describeIssueActivity } from './issue-activity-model'

const event = (metadata: Record<string,string>, type = 'issue.updated'): ActivityEvent => ({ id: 'event', type, actor: viewer, createdAt: '2026-09-08T06:00:00Z', metadata })

describe('issue activity projection', () => {
  it('hides description autosaves and bookkeeping without modifying stored metadata', () => {
    const metadata = { description: 'updated', descriptionBefore: 'Private old text', descriptionStateBefore: '{"type":"doc"}', documentContent: 'doc-id' }
    expect(describeIssueActivity(event(metadata))).toBeNull()
    expect(metadata.descriptionBefore).toBe('Private old text')
    expect(describeIssueActivity(event({ subscribers: 'user-1', sortOrder: '12' }))).toBeNull()
    expect(describeIssueActivity(event({}))).toBeNull()
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

  it('uses compact localized timestamps', () => {
    const now = Date.parse('2026-09-08T06:42:00Z')
    expect(activityTimeLabel('2026-09-08T06:00:00Z', now, 'en-US')).toBe('42m ago')
    expect(activityTimeLabel('2026-09-08T06:00:00Z', now, 'zh-CN')).toBe('42分钟前')
    expect(activityTimeLabel('2026-09-08T06:41:50Z', now, 'en-US')).toBe('just now')
  })
})
