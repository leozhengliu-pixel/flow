import { describe, expect, it } from 'vitest'
import type { ActivityEvent } from '@/types/flow'
import { makeBootstrap, viewer } from '@/test/fixtures'
import { loadChineseDictionary, translateToChinese } from '@/i18n/translate'
import { activityExpandedTimeLabel, activityIds, activityPartsText, activityTimeLabel, describeIssueActivity, issueActivityEntries, withoutSessionDelegations } from './issue-activity-model'
import { activityCollapseReasons, groupIssueActivityEntries, orderIssueActivityEntries, summarizeIssueActivity, partitionIssueActivity, activityBlockExpanded, expandActivityBlock } from './issue-event-groups'

const now = Date.parse('2026-11-10T20:00:00Z')
const event = (metadata: Record<string, string>, type = 'issue.updated', id = 'event'): ActivityEvent => ({ id, type, actor: viewer, createdAt: '2026-11-01T06:00:00Z', metadata })
const context = makeBootstrap()
const project = context.projects[0], label = context.labels[0], target = context.issues[0]
const text = (metadata: Record<string, string>, type?: string) => describeIssueActivity(event(metadata, type), context)

describe('record sentence templates', () => {
  it.each([
    [{ state: 'Done', stateBefore: 'Todo' }, 'moved from Todo to Done'],
    [{ state: 'Done' }, 'moved to Done'],
    [{ priority: 'High' }, 'set priority to High'],
    [{ priority: 'High', priorityBefore: 'Low' }, 'raised priority from Low to High'],
    [{ priority: 'Low', priorityBefore: 'Urgent' }, 'lowered priority from Urgent to Low'],
    [{ priority: 'No priority', priorityBefore: 'High' }, 'removed priority'],
    [{ priority: '2', priorityBefore: '4' }, 'raised priority from Low to High'],
    [{ assignee: viewer.id }, 'self-assigned the issue'],
    [{ assignee: '' }, 'removed assignee'],
    [{ assignee: 'missing' }, 'assigned to another user'],
    [{ assignee: 'missing', assigneeBefore: viewer.id }, 're-assigned to another user'],
    [{ assignee: 'missing', previousAssignee: viewer.id }, 're-assigned to another user'],
    [{ estimate: '-1', estimateBefore: '2' }, 'removed estimate'],
    [{ delegate: viewer.id }, 'self-delegated the issue'],
    [{ delegate: 'missing' }, 'delegated to another user'],
    [{ delegate: '', delegateBefore: viewer.id }, `unassigned ${viewer.displayName}`],
    [{ estimate: '2' }, 'estimated complexity as 2 points'],
    [{ estimate: '1' }, 'estimated complexity as 1 point'],
    [{ estimate: '4', estimateBefore: '2' }, 'increased estimate from 2 points to 4 points'],
    [{ estimate: '1', estimateBefore: '4' }, 'lowered estimate from 4 points to 1 point'],
    [{ estimate: '0', estimateBefore: '1' }, 'removed estimate'],
    [{ estimate: 'XL' }, 'estimated complexity as XL'],
    [{ dueDate: '2026-11-11' }, 'set the due date to Nov 11'],
    [{ dueDate: '2026-11-17', dueDateBefore: '2026-11-11' }, 'changed the due date from Nov 11 to Nov 17'],
    [{ dueDate: '' }, 'removed due date'],
    [{ title: 'Updated', titleBefore: 'Old' }, 'changed title to Updated'],
    [{ project: project.id }, `added to ${/project/i.test(project.name) ? '' : 'project '}${project.name}`],
    [{ project: '', projectBefore: project.id }, `removed from ${/project/i.test(project.name) ? '' : 'project '}${project.name}`],
    [{ project: project.id, projectBefore: 'missing' }, `changed project from another project to ${project.name}`],
    [{ projectMilestone: 'missing', project: project.id, projectBefore: project.id }, `added to milestone another milestone in project ${project.name}`],
    [{ projectMilestone: '', projectMilestoneBefore: 'old' }, 'removed from milestone another milestone in project another project'],
    [{ projectMilestone: 'new', projectMilestoneBefore: 'old' }, 'changed milestone from another milestone to another milestone'],
    [{ cycle: 'new' }, 'added issue to another cycle'],
    [{ cycle: '', cycleBefore: 'old' }, 'removed issue from another cycle'],
    [{ cycle: 'new', cycleBefore: 'old' }, 'moved issue to another cycle'],
    [{ parent: target.id }, `set parent to ${target.identifier}`],
    [{ parent: '', parentBefore: target.id }, `removed parent ${target.identifier}`],
    [{ parent: target.id, parentBefore: 'old' }, `changed parent issue from another issue to ${target.identifier}`],
    [{ team: 'new', teamName: 'Design', teamBeforeName: 'Engineering' }, 'changed team from Engineering to Design'],
    [{ archived: 'true' }, 'archived the issue'],
    [{ archived: 'false' }, 'restored the issue'],
    [{ trashed: 'true' }, 'deleted the issue'],
    [{ trashed: 'false' }, 'restored the issue'],
    [{ recurrence: '' }, 'stopped the recurring schedule'],
    [{ recurrence: 'weekly' }, 'changed the recurring schedule'],
    [{ sla: '2d' }, 'set the SLA to 2d'],
    [{ sla: '3d', slaBefore: '2d' }, 'changed the SLA from 2d to 3d'],
    [{ sla: '2d', slaReset: 'true' }, 'reset SLA to 2d'],
    [{ sla: '' }, 'removed the SLA'],
    [{ state: 'Canceled', automation: 'autoCloseStale' }, 'moved to Canceled due to inactivity'],
    [{ addedLabelIds: JSON.stringify([label.id]) }, `added label ${label.name}`],
    [{ removedLabelIds: JSON.stringify([label.id]) }, `removed label ${label.name}`],
    [{ labels: label.id }, `changed labels to ${label.name}`],
  ] satisfies [Record<string, string>, string][])('%j → %s', (metadata, expected) => expect(text(metadata)).toBe(expected))

  it.each([
    [{}, 'created'], [{ templateName: 'Bug' }, 'created the Bug'], [{ recurringFrom: 'FLO-1' }, 'created as part of a recurring series'],
    [{ importer: 'CSV' }, 'created in CSV'], [{ email: 'true' }, 'created via email'], [{ sourceComment: 'id' }, 'created from a comment'],
    [{ sourceThread: 'id' }, 'created from a thread'], [{ sourceReview: 'PR #2' }, 'created from a review comment on PR #2'],
    [{ integration: 'GitLab' }, 'created from GitLab'], [{ addedBy: viewer.id }, `created, added by ${viewer.displayName}`],
  ] satisfies [Record<string, string>, string][])('creation %j', (metadata, expected) => expect(text(metadata, 'issue.created')).toBe(expected))

  it.each([
    ['blocks', 'marked this issue as blocking', 'unmarked this issue as blocking'],
    ['blocked_by', 'marked this issue as blocked by', 'unmarked this issue as being blocked by'],
    ['related', 'added related issue', 'removed related issue'],
    ['duplicate', 'marked this issue as a duplicate of', 'unmarked this issue as a duplicate of'],
    ['sub_issue_of', 'set parent to', 'removed parent'],
  ])('relation %s', (relationType, added, removed) => {
    expect(text({ relationType, direction: 'outgoing', relatedIssueId: target.id }, 'issue.relation_added')).toBe(`${added} ${target.identifier}`)
    expect(text({ relationType, direction: 'outgoing', relatedIssueId: target.id }, 'issue.relation_removed')).toBe(`${removed} ${target.identifier}`)
  })
  it('renders inverse relations and hides parent-side and duplicate status audit rows', () => {
    // The other issue's event stores the type as that issue sees it ("blocked_by" for a "blocks" relation).
    expect(text({ type: 'blocked_by', relationType: 'blocked_by', direction: 'inverse', relatedIssueId: target.id }, 'issue.relation_added')).toBe(`marked this issue as blocked by ${target.identifier}`)
    expect(text({ type: 'blocks', relationType: 'blocks', direction: 'inverse', relatedIssueId: target.id }, 'issue.relation_removed')).toBe(`unmarked this issue as blocking ${target.identifier}`)
    expect(text({ relationType: 'duplicate', direction: 'inverse', relatedIssueId: target.id }, 'issue.relation_added')).toBe(`marked ${target.identifier} as a duplicate of this issue`)
    expect(text({ type: 'parent_of', relationType: 'parent_of', direction: 'inverse', relatedIssueId: target.id }, 'issue.relation_added')).toBeNull()
    expect(text({ relationId: 'relation' }, 'issue.relation_removed')).toBe('removed an issue relation')
    expect(text({ type: 'parent_of' }, 'issue.relation_added')).toBeNull()
    expect(text({ state: 'Duplicate', viaDuplicate: 'true' })).toBeNull()
  })
  it('uses contract link fields and preserves Flow session and release rows', () => {
    expect(text({ linkTitle: 'A link', linkUrl: 'https://example.org', linkSource: 'url' }, 'attachment.created')).toBe('linked A link')
    expect(text({ title: 'Legacy link' }, 'attachment.created')).toBe('linked Legacy link')
    expect(text({}, 'attachment.deleted')).toBe('removed an attachment')
    expect(text({ added: 'v2', removed: 'v1' }, 'issue.releases_updated')).toBe('added to release v2 and removed from release v1')
    expect(text({}, 'issue.coding_session_archived')).toBe('Session with Flow archived')
    expect(text({ environment: 'Staging' }, 'issue.preview_ready')).toBe('deployed preview Staging')
    expect(text({}, 'issue.sla_breached')).toBe('SLA breached')
    expect(text({}, 'issue.archived')).toBe('archived the issue')
    expect(text({}, 'issue.restored')).toBe('restored the issue')
    expect(text({}, 'issue.deleted')).toBe('deleted the issue')
    expect(text({ importer: 'CSV' }, 'issue.imported')).toBe('imported issue from CSV')
    expect(text({ loopName: 'Review' }, 'issue.loop_run_created')).toBe('Review is waiting to run…')
  })
  it('splits in field order, pairing only small changes, with record time and stable ids', () => {
    const rows = issueActivityEntries(event({ state: 'Done', assignee: viewer.id, project: project.id, priority: 'Medium', estimate: '2', addedLabelIds: JSON.stringify([label.id]), title: 'New title', dueDate: '2026-11-11' }), context)
    expect(rows.map(row => row.entryType)).toEqual(['state-change', 'aggregate', 'aggregate', 'label-added', 'title-change', 'due-date'])
    expect(activityPartsText(rows[1].description)).toContain('self-assigned the issue and added to ')
    expect(activityPartsText(rows[2].description)).toBe('set priority to Medium and estimated complexity as 2 points')
    expect(new Set(rows.map(row => row.id)).size).toBe(rows.length)
    expect(rows.every(row => row.recordId === 'event' && row.createdAt === '2026-11-01T06:00:00Z')).toBe(true)
  })
  it('cancels round trips and hides bookkeeping without mutating metadata', () => {
    const metadata = { state: 'Todo', stateBefore: 'Todo', addedLabelIds: JSON.stringify([label.id]), removedLabelIds: JSON.stringify([label.id]), description: 'updated', subscribers: viewer.id }
    expect(text(metadata)).toBeNull()
    expect(metadata.stateBefore).toBe('Todo')
    expect(text({}, 'comment.created')).toBeNull()
    expect(text({}, 'issue.reacted')).toBeNull()
  })
  it('accepts actual label arrays and packs the final three of seven small clauses together', () => {
    expect(activityIds([label.id, 42])).toEqual([label.id])
    const changed = event({ delegate: 'agent', assignee: viewer.id, project: project.id, priority: 'Medium', estimate: '2' })
    changed.metadata = { ...changed.metadata, addedLabelIds: [label.id], removedLabelIds: ['removed'] } as unknown as ActivityEvent['metadata']
    const rows = issueActivityEntries(changed, context)
    expect(rows).toHaveLength(3)
    expect(rows.map(row => row.entryType)).toEqual(['aggregate', 'aggregate', 'aggregate'])
    expect(activityPartsText(rows[2].description)).toBe(`estimated complexity as 2 points, added label ${label.name} and removed label another label`)
  })
  it('keeps Flow agent assignment attribution and suppresses delegation represented by a session', () => {
    expect(describeIssueActivity({ ...event({ assignee: viewer.id }), botActor: { type: 'agent', name: 'Flow', onBehalfOfId: viewer.id } }, context)).toBe(`assigned to ${viewer.displayName}`)
    expect(withoutSessionDelegations([event({ delegate: viewer.id, agentSessionId: 'session', state: 'Todo' })], [{ id: 'session', createdAt: '2026-11-01T06:00:00Z' }])[0].metadata).toEqual({ state: 'Todo' })
  })
  it('translates sentences while leaving entity names intact', async () => {
    await loadChineseDictionary()
    expect(describeIssueActivity(event({ assignee: viewer.id }), context, translateToChinese)).toBe('将此事项分配给自己')
  })
})

describe('families and age collapse', () => {
  const entries = (count: number) => Array.from({ length: count }, (_, i) => issueActivityEntries(event({ title: `Title ${i}` }, 'issue.updated', `event-${i}`))[0])
  it.each([['only', 8, 0], ['only', 9, 3], ['first', 4, 0], ['first', 5, 2], ['last', 3, 0], ['last', 4, 2], ['middle', 1, 0], ['middle', 2, 2]] as const)('%s threshold %i', (position, count, hidden) => expect(partitionIssueActivity(entries(count), position, now).hidden).toHaveLength(hidden))
  it('keeps a four-hour boundary, recent comment context, and hidden runs smaller than two', () => {
    const rows = entries(9)
    rows[5].createdAt = new Date(now - 4 * 3600_000).toISOString()
    expect(partitionIssueActivity(rows, 'only', now).hidden).toHaveLength(2)
    rows[4].createdAt = rows[5].createdAt
    expect(partitionIssueActivity(rows, 'only', now).hidden).toHaveLength(0)
    const comment = now - 3 * 86400_000
    rows[0].createdAt = new Date(comment - 5 * 60_000).toISOString()
    expect(partitionIssueActivity(rows, 'middle', now, new Date(comment).toISOString()).hidden).toHaveLength(0)
  })
  it('groups status only for the same actor without an elapsed-time cap', () => {
    const a = issueActivityEntries(event({ stateBefore: 'Todo', state: 'Doing' }, 'issue.updated', 'a'))[0]
    const b = issueActivityEntries({ ...event({ stateBefore: 'Doing', state: 'Done' }, 'issue.updated', 'b'), createdAt: new Date(now).toISOString() })[0]
    const result = groupIssueActivityEntries([a, b], { now, last: true })
    expect(result[0].members).toHaveLength(2)
    expect(activityPartsText(result[0].description)).toBe('moved to Doing, then to Done')
    expect(groupIssueActivityEntries([a, { ...b, actor: { ...viewer, id: 'other' } }], { now, last: true })).toHaveLength(2)
  })
  it('collects old title families across intervening rows only in completed blocks', () => {
    const titles = entries(2)
    titles[1] = { ...titles[1], actor: { ...viewer, id: 'other' } }
    const middle = issueActivityEntries(event({ estimate: '2' }, 'issue.updated', 'estimate'))[0]
    expect(groupIssueActivityEntries([titles[0], middle, titles[1]], { now, last: true })).toHaveLength(3)
    const grouped = groupIssueActivityEntries([titles[0], middle, titles[1]], { now, last: false })
    expect(grouped).toHaveLength(2)
    expect(activityPartsText(grouped[1].description)).toBe('changed title twice')
  })
  it('summarizes hidden events by weight, then by first appearance, grouped by action', () => {
    const rows = (changes: [string, Record<string, string>][]) => changes.flatMap(([type, metadata], i) => issueActivityEntries(event(metadata, type, `reason-${i}`), context))
    expect(activityCollapseReasons(rows([['issue.updated', { priority: 'High' }], ['issue.updated', { state: 'Done' }], ['issue.updated', { addedLabelIds: JSON.stringify([label.id]) }], ['attachment.created', { linkTitle: 'Doc' }]]))).toBe(': changed priority, status and labels, added links…')
    expect(activityCollapseReasons(rows([['issue.updated', { dueDate: '2026-11-11' }], ['issue.relation_added', { relationType: 'related', relatedIssueId: target.id }], ['issue.updated', { title: 'A' }], ['issue.updated', { estimate: '2' }], ['issue.updated', { assignee: viewer.id, priority: 'High' }], ['issue.sla_breached', {}]]))).toBe(': breached SLA, changed assignee, priority, due date and title…')
    expect(activityCollapseReasons(rows([['issue.updated', { archived: 'true' }], ['issue.updated', { state: 'Done' }]]))).toBe(': changed status, archived issue…')
    expect(activityCollapseReasons(rows([['issue.created', {}], ['issue.created', {}]]))).toBe('')
  })
  it('remembers expanded blocks for fifteen minutes', () => {
    expandActivityBlock('block', now)
    expect(activityBlockExpanded('block', now + 15 * 60_000)).toBe(true)
    expect(activityBlockExpanded('block', now + 15 * 60_000 + 1)).toBe(false)
  })
  it('keeps counted label text in groups and groups paired add/remove clauses', () => {
    const many = issueActivityEntries(event({ addedLabelIds: JSON.stringify(['a', 'b', 'c', 'd', 'e', 'f']) }, 'issue.updated', 'many'), context)[0]
    const one = issueActivityEntries(event({ removedLabelIds: JSON.stringify([label.id]) }, 'issue.updated', 'one'), context)[0]
    expect(activityPartsText(summarizeIssueActivity([many, one]))).toBe(`added 6 labels and removed label ${label.name}`)
    const paired = issueActivityEntries(event({ addedLabelIds: JSON.stringify([label.id]), removedLabelIds: JSON.stringify(['other']) }, 'issue.updated', 'paired'), context)[0]
    const grouped = groupIssueActivityEntries([paired, one], { now, last: true })
    expect(grouped).toHaveLength(1)
    expect(grouped[0].members).toHaveLength(2)
    expect(activityPartsText(grouped[0].description)).toBe(`added and then removed ${label.name} and removed another label`)
  })
  it('preserves context at the five-minute/four-day comment boundary', () => {
    const comment = now - 4 * 86400_000
    const rows = entries(3).map((row, i) => ({ ...row, createdAt: new Date(comment - 5 * 60_000 + i).toISOString() }))
    expect(partitionIssueActivity(rows, 'middle', now, new Date(comment).toISOString()).hidden).toHaveLength(0)
    expect(partitionIssueActivity(rows, 'middle', now, new Date(comment - 1).toISOString()).hidden).toHaveLength(3)
  })
  it('brings a new record’s matching family beside the block’s last entry', () => {
    const a = issueActivityEntries(event({ priority: 'Low' }, 'issue.updated', 'a'), context)
    const b = issueActivityEntries(event({ state: 'Done', priority: 'High' }, 'issue.updated', 'b'), context)
    expect(orderIssueActivityEntries([...a, ...b]).map(row => row.entryType)).toEqual(['priority', 'priority', 'state-change'])
  })
  it('folds rapid title edits with the original before-value', () => {
    const a = issueActivityEntries(event({ title: 'One', titleBefore: 'Original' }, 'issue.updated', 'a'))[0]
    const b = { ...issueActivityEntries(event({ title: 'Two', titleBefore: 'One' }, 'issue.updated', 'b'))[0], createdAt: new Date(Date.parse(a.createdAt) + 60_000).toISOString() }
    const [row] = groupIssueActivityEntries([a, b], { now, last: true })
    expect(row.metadata.titleBefore).toBe('Original')
    expect(row.members).toBeUndefined()
    expect(activityPartsText(row.description)).toBe('changed title to Two')
  })
  it('uses workflow types from context for backward groups without extra contract fields', () => {
    const changes = [{ stateBefore: 'Done', state: 'In Progress', stateType: 'started' }, { stateBefore: 'In Progress', state: 'Backlog', stateType: 'backlog' }, { stateBefore: 'Backlog', state: 'Todo', stateType: 'unstarted' }]
    const rows = changes.map((metadata, index) => issueActivityEntries(event(metadata, 'issue.updated', String(index)), context)[0])
    expect(activityPartsText(summarizeIssueActivity(rows))).toBe('moved back from Done to Todo')
  })
  it.each([
    [[{ stateBefore: 'Todo', state: 'Doing' }, { stateBefore: 'Doing', state: 'Todo' }], 'moved to Doing, then back to Todo'],
    [[{ stateBefore: 'Todo', stateBeforeType: 'unstarted', state: 'Doing' }, { state: 'Review' }, { state: 'Done', stateType: 'completed' }], 'moved from Todo to Done'],
    [[{ stateBefore: 'Done', stateBeforeType: 'completed', state: 'Review' }, { state: 'Doing' }, { state: 'Todo', stateType: 'unstarted' }], 'moved back from Done to Todo'],
    [[{ priority: 'High' }, { priority: 'Low' }], 'set priority to High, then set to Low'],
    [[{ priority: '2' }, { priority: '4' }], 'set priority to High, then set to Low'],
    [[{ priority: 'High' }, { priority: 'No priority' }], 'set priority to High, then removed priority'],
    [[{ priority: 'High' }, { priority: 'Low' }, { priority: 'Urgent' }], 'changed priority a few times before setting it to Urgent'],
    [[{ priority: 'High' }, { priority: 'Low' }, { priority: 'No priority' }], 'changed priority a few times before removing it'],
    [[{ title: 'One' }, { title: 'Two' }], 'changed title twice'],
    [[{ title: 'One' }, { title: 'Two' }, { title: 'Three' }], 'changed title three times'],
    [[{ cycle: 'one' }, { cycle: 'two' }], 'added issue to another cycle and moved to another cycle'],
    [[{ cycle: 'one' }, { cycle: '', cycleBefore: 'one' }], 'added issue to another cycle and then removed it from another cycle'],
    [[{ cycle: 'one' }, { cycle: 'two' }, { cycle: 'three' }], 'moved issue through three cycles to another cycle'],
    [[{ addedLabelIds: JSON.stringify([label.id]) }, { removedLabelIds: JSON.stringify([label.id]) }], `added and then removed ${label.name}`],
    [[{ removedLabelIds: JSON.stringify([label.id]) }, { addedLabelIds: JSON.stringify([label.id]) }], `removed and then added ${label.name}`],
  ] satisfies [Record<string, string>[], string][])('group %j', (changes, expected) => {
    const rows = changes.map((change, i) => issueActivityEntries(event(change as Record<string, string>, 'issue.updated', String(i)), context)[0])
    expect(activityPartsText(summarizeIssueActivity(rows))).toBe(expected)
  })
})

describe('relative timestamps', () => {
  it.each([[59, 'just now'], [90, '2min ago'], [4 * 60, '4min ago'], [3600 + 39 * 60, '1h ago'], [3600 + 40 * 60, '2h ago'], [8.5 * 3600, '9h ago'], [86400 * 1.65, '1d ago'], [86400 * 1.66, '2d ago'], [7 * 86400, '1w ago'], [14 * 86400, '2w ago'], [40 * 86400, '6w ago'], [90 * 86400, '3mo ago'], [366 * 86400, '1y ago']])('%i seconds → %s', (seconds, expected) => expect(activityTimeLabel(new Date(now - seconds * 1000).toISOString(), now, 'en-US')).toBe(expected))
  it('localizes time', () => expect(activityTimeLabel(new Date(now - 42 * 60_000).toISOString(), now, 'zh-CN')).toBe('42分钟前'))
  it('uses long times for today/yesterday and dates for older expanded rows', () => {
    expect(activityExpandedTimeLabel(new Date(now - 5 * 60_000).toISOString(), now, 'en-US')).toBe('5 minutes ago')
    expect(activityExpandedTimeLabel('2026-11-01T12:00:00Z', now, 'en-US')).toBe('Nov 1')
  })
})
