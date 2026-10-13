import assert from 'node:assert/strict'
import { test } from 'node:test'
import { newRunContext } from '../src/context.js'
import { approvalKind, isApprovalWord } from '../src/policy.js'
import { initialGroups, toolOffered } from '../src/tool-groups.js'

test('bulk delete in one turn asks once; a single delete does not', () => {
  const context = newRunContext('s', 'Delete all sub-issues of DEV-1', [])
  context.turns.push([
    { callId: 'a', name: 'delete_issue', arguments: '{"id":"DEV-2"}' },
    { callId: 'b', name: 'delete_issue', arguments: '{"id":"DEV-3"}' },
  ])
  context.turns.push([{ callId: 'c', name: 'delete_issue', arguments: '{"id":"DEV-4"}' }])
  assert.equal(approvalKind(context, 'delete_issue', { id: 'DEV-2' }, 'a'), 'delete')
  assert.equal(approvalKind(context, 'delete_issue', { id: 'DEV-4' }, 'c'), null)
})

test('sequential deletes ask before the second; approval covers the rest of the request', () => {
  const context = newRunContext('s', 'Delete DEV-2 and DEV-3', [])
  context.turns.push([{ callId: 'a', name: 'delete_issue', arguments: '{"id":"DEV-2"}' }])
  assert.equal(approvalKind(context, 'delete_issue', { id: 'DEV-2' }, 'a'), null)
  context.deletesDone = 1
  context.turns.push([{ callId: 'b', name: 'delete_issue', arguments: '{"id":"DEV-3"}' }])
  assert.equal(approvalKind(context, 'delete_issue', { id: 'DEV-3' }, 'b'), 'delete')
  context.deletesApproved = true
  assert.equal(approvalKind(context, 'delete_issue', { id: 'DEV-3' }, 'b'), null)
})

test('status updates: asks only for a new update the user did not ask to post', () => {
  assert.equal(approvalKind(newRunContext('s', 'How is the project going?', []), 'save_status_update', { body: 'x' }), 'publish')
  assert.equal(approvalKind(newRunContext('s', 'Post an update saying we are on track', []), 'save_status_update', { body: 'x' }), null)
  assert.equal(approvalKind(newRunContext('s', '发布一个进展更新', []), 'save_status_update', { body: 'x' }), null)
  assert.equal(approvalKind(newRunContext('s', 'How is it going?', []), 'save_status_update', { id: 'upd_1', body: 'x' }), null)
})

test('connector tools always ask; approval words skip the other rules', () => {
  assert.equal(approvalKind(newRunContext('s', 'yes', []), 'external_abc', {}), 'connector')
  const context = newRunContext('s', '批准', [])
  context.turns.push([{ callId: 'a', name: 'delete_issue', arguments: '{"ids":["DEV-2","DEV-3"]}' }])
  assert.equal(approvalKind(context, 'delete_issue', { ids: ['DEV-2', 'DEV-3'] }, 'a'), null)
  assert.ok(isApprovalWord('Go ahead!'))
  assert.ok(!isApprovalWord('no'))
})

test('tool groups: core always, groups by intent, hidden never', () => {
  assert.ok(toolOffered('get_issue', []))
  assert.ok(!toolOffered('save_issue', []))
  assert.deepEqual(initialGroups('What is in Triage?').includes('issue_changes'), true) // "triage" is a change verb, as in Go
  assert.ok(initialGroups('Create a project for Q4').includes('project_changes'))
  assert.ok(!toolOffered('report_progress', ['issue_changes']))
})
