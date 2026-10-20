import assert from 'node:assert/strict'
import { test } from 'node:test'
import { newRunContext } from '../src/context.js'
import { approvalKind, isApprovalWord } from '../src/policy.js'
import { initialGroups, toolOffered } from '../src/tool-groups.js'

test('deletes and status updates run without approval', () => {
  const context = newRunContext('s', 'Delete all sub-issues of DEV-1, then write a project update', [])
  context.turns.push([
    { callId: 'a', name: 'delete_issue', arguments: '{"id":"DEV-2"}' },
    { callId: 'b', name: 'delete_issue', arguments: '{"ids":["DEV-3","DEV-4"]}' },
  ])
  assert.equal(approvalKind(context, 'delete_issue'), null)
  assert.equal(approvalKind(context, 'save_status_update'), null)
})

test('connector tools ask only when their connector is set to always ask', () => {
  assert.equal(approvalKind(newRunContext('s', 'Search the code', []), 'external_abc'), null)
  const context = newRunContext('s', 'Search the code', [])
  context.alwaysAskTools = ['external_abc']
  assert.equal(approvalKind(context, 'external_abc'), 'connector')
  assert.equal(approvalKind(context, 'external_def'), null)
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
