import { expect } from '@playwright/test'
import type { BootstrapData, Issue } from '../src/types/flow'
import { test } from './workspace-fixture'

test('SLA rules attach a live deadline, update it, retire it and record the achieved result', async ({ page, request, workspaceKey }) => {
  test.setTimeout(90_000)
  const headers = { 'X-Workspace-Key': workspaceKey }
  const bootstrap = async () => {
    const response = await request.get('/api/bootstrap', { headers })
    expect(response.status()).toBe(200)
    return await response.json() as BootstrapData
  }
  const initial = await bootstrap()
  const team = initial.teams[0]
  const open = initial.states.find(state => state.type === 'unstarted' && (!state.teamId || state.teamId === team.id))!
  const completed = initial.states.find(state => state.type === 'completed' && (!state.teamId || state.teamId === team.id))!

  await page.goto(`/${workspaceKey}/settings/sla`)
  const toggle = page.getByRole('checkbox', { name: 'Enable SLAs' })
  await expect(toggle).not.toBeChecked()
  await toggle.click()
  await expect(toggle).toBeChecked()
  await page.getByRole('button', { name: 'Add rule', exact: true }).click()
  const add = page.getByRole('dialog', { name: 'Add rule' })
  await add.getByRole('textbox', { name: 'Amount' }).fill('24')
  await add.getByRole('button', { name: 'Add rule', exact: true }).click()
  await expect(add).toBeHidden()
  await expect(page.locator('.sla-rule')).toHaveCount(1)

  const rules = (await bootstrap()).slaRules
  expect(rules).toHaveLength(1)
  expect(rules[0]).toMatchObject({ action: 'add', targetMinutes: 1440, filters: { priority: 1 } })
  const created = await request.post('/api/issues', {
    headers,
    data: { title: 'Flow parity SLA lifecycle', teamId: team.id, stateId: open.id, priority: 1, assigneeId: initial.viewer.id },
  })
  expect(created.status()).toBe(201)
  const issue = await created.json() as Issue
  const slaForIssue = async () => (await bootstrap()).issueSlas.findLast(sla => sla.issueId === issue.id && sla.status !== 'removed')
  const first = await slaForIssue()
  expect(first).toMatchObject({ issueId: issue.id, ruleId: rules[0].id, status: 'active' })
  expect(Date.parse(first!.dueAt) - Date.parse(first!.startedAt)).toBe(24 * 60 * 60_000)

  await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
  const badge = page.locator('.issue-sla:visible').first()
  await expect(badge).toBeVisible()
  await expect(badge).toHaveAttribute('data-status', 'high')
  await expect(badge).toContainText(/remaining/)

  await page.getByRole('button', { name: 'Change SLA…', exact: true }).first().click()
  await page.getByRole('option', { name: '48 hours', exact: true }).click()
  await expect(page.locator('.issue-sla-command:visible')).toHaveCount(0)
  await expect(badge).toHaveAttribute('data-status', 'medium')
  const manual = await slaForIssue()
  expect(manual).toMatchObject({ id: first!.id, manual: true, status: 'active' })
  expect(Date.parse(manual!.dueAt) - Date.parse(manual!.startedAt)).toBeGreaterThan(48 * 60 * 60_000 - 5000)
  expect(Date.parse(manual!.dueAt) - Date.parse(manual!.startedAt)).toBeLessThanOrEqual(48 * 60 * 60_000)
  await page.getByRole('button', { name: 'Change SLA…', exact: true }).first().click()
  await page.getByRole('option', { name: 'Remove SLA', exact: true }).click()
  await expect(page.locator('.issue-sla-command:visible')).toHaveCount(0)
  await expect(page.locator('.issue-sla:visible')).toHaveCount(0)
  expect(await slaForIssue()).toBeUndefined()
  await page.reload()
  await expect(page.getByRole('textbox', { name: 'Issue title', exact: true })).toBeVisible()
  await expect(page.locator('.issue-sla:visible')).toHaveCount(0)
  expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: { priority: 3 } })).status()).toBe(200)
  expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: { priority: 1 } })).status()).toBe(200)
  const automatic = await slaForIssue()
  expect(automatic).toMatchObject({ ruleId: rules[0].id, status: 'active' })
  expect(automatic!.manual).not.toBe(true)

  await page.goto(`/${workspaceKey}/settings/sla`)
  await page.locator('.sla-rule').hover()
  await page.getByRole('button', { name: 'Edit configuration' }).click()
  const edit = page.getByRole('dialog', { name: 'Edit rule' })
  await edit.getByRole('textbox', { name: 'Amount' }).fill('12')
  await edit.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(edit).toBeHidden()
  const edited = await slaForIssue()
  expect(edited!.id).toBe(automatic!.id)
  expect(edited!.startedAt).toBe(automatic!.startedAt)
  expect(Date.parse(edited!.dueAt) - Date.parse(edited!.startedAt)).toBe(12 * 60 * 60_000)

  expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: { priority: 3 } })).status()).toBe(200)
  await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
  await expect(page.getByRole('textbox', { name: 'Issue title', exact: true })).toBeVisible()
  await expect(page.locator('.issue-sla:visible')).toHaveCount(0)
  expect(await slaForIssue()).toBeUndefined()

  await page.getByRole('button', { name: 'Set SLA…', exact: true }).first().click()
  await page.getByRole('option', { name: '12 hours', exact: true }).click()
  await expect(page.locator('.issue-sla-command:visible')).toHaveCount(0)
  await expect(badge).toHaveAttribute('data-status', 'high')
  await expect(badge).toContainText(/12h remaining/)
  expect(await slaForIssue()).toMatchObject({ manual: true, ruleId: '', status: 'active' })
  await page.getByRole('button', { name: 'Change SLA…', exact: true }).first().click()
  await page.getByRole('option', { name: 'Remove SLA', exact: true }).click()
  await expect(page.locator('.issue-sla-command:visible')).toHaveCount(0)
  await expect(page.locator('.issue-sla:visible')).toHaveCount(0)
  expect(await slaForIssue()).toBeUndefined()

  expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: { priority: 1 } })).status()).toBe(200)
  const restarted = await slaForIssue()
  expect(restarted!.id).not.toBe(automatic!.id)
  await page.reload()
  await expect(badge).toHaveAttribute('data-status', 'high')

  expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: { stateId: completed.id } })).status()).toBe(200)
  await page.reload()
  await expect(badge).toHaveAttribute('data-status', 'achieved')
  await expect(badge).toContainText(/Achieved/)
  const finished = await slaForIssue()
  expect(finished!.status).toBe('completed')
  expect(Date.parse(finished!.completedAt!)).toBeLessThan(Date.parse(finished!.dueAt))

  await page.goto(`/${workspaceKey}/settings/sla`)
  await page.locator('.sla-rule').hover()
  await page.getByRole('button', { name: 'Delete configuration' }).click()
  await page.getByRole('dialog').filter({ hasText: 'Delete this SLA?' }).getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(page.locator('.sla-rule')).toHaveCount(0)
  await toggle.click()
  await expect(toggle).not.toBeChecked()
  await page.reload()
  await expect(toggle).not.toBeChecked()
  expect((await bootstrap()).slaRules).toHaveLength(0)
})
