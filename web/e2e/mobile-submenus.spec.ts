import { expect, type Locator, type Page } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, FlowDocument, Issue, Project } from '../src/types/flow'

test.use({ isMobile: true, hasTouch: true, colorScheme: 'dark' })

async function fitsPhone(menu: Locator, viewport: { width: number; height: number }) {
  await expect(menu).toBeVisible()
  await menu.evaluate(async element => { await Promise.all(element.getAnimations().map(animation => animation.finished)) })
  const box = (await menu.boundingBox())!
  expect(box.x).toBeCloseTo(16, 0)
  expect(box.width).toBeCloseTo(viewport.width - 32, 0)
  expect(box.y).toBeGreaterThanOrEqual(0)
  expect(box.y + box.height).toBeLessThanOrEqual(viewport.height)
}

async function shot(page: Page, name: string, width: number) {
  if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/${name}-${width}.png`, scale: 'css', animations: 'disabled' })
}

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test.describe(`phone submenu levels at ${viewport.width}px`, () => {
    test.use({ viewport })

    test('Snooze replaces the row menu, returns, and persists a preset', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const settings = await request.patch('/api/workspace/preferences', { headers, data: { welcomeMessageTitle: 'Phone snooze', welcomeMessage: 'Phone submenu check', welcomeMessageEnabled: true } })
      expect(settings.ok()).toBe(true)
      expect((await request.post('/api/workspace/welcome-message/test', { headers })).ok()).toBe(true)
      await page.goto(`/${workspaceKey}/inbox`)
      const row = page.locator('.flow-inbox-row').first()
      await row.click({ button: 'right', position: { x: 154, y: 28 } })
      const parent = page.getByRole('menu', { name: 'Notification actions' })
      await parent.getByRole('menuitem', { name: /^Snooze/ }).tap()
      const menu = page.getByRole('menu', { name: 'Snooze', exact: true })
      await fitsPhone(menu, viewport)
      await expect(parent).toBeHidden()
      await menu.getByRole('button', { name: 'Back', exact: true }).tap()
      await expect(parent).toBeVisible()
      await parent.getByRole('menuitem', { name: /^Snooze/ }).tap()
      await fitsPhone(menu, viewport)
      await shot(page, 'snooze', viewport.width)
      const start = Date.now()
      const mutation = page.waitForResponse(response => response.request().method() === 'PATCH' && /\/api\/notifications\//.test(response.url()))
      await menu.getByRole('menuitem', { name: /^In 3 hours/ }).tap()
      const response = await mutation
      expect(response.ok()).toBe(true)
      const saved = await response.json() as { snoozedUntil: string }
      expect(Math.abs(Date.parse(saved.snoozedUntil) - start - 3 * 3600_000)).toBeLessThan(10_000)
      await expect(row).toHaveCount(0)
      await page.reload()
      await expect(page.locator('.flow-inbox-row')).toHaveCount(0)
    })

    test('issue Labels and two nested Move levels stay inside the viewport', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
      const team = data.teams[0]
      const response = await request.post('/api/issues', { headers, data: { title: 'Phone row submenu', teamId: team.id } })
      expect(response.status()).toBe(201)
      const issue = await response.json() as Issue
      const other = await request.post(`/api/workspaces/${workspaceKey}/teams`, { headers, data: { name: 'Phone destination', key: 'DST' } })
      expect(other.status()).toBe(201)
      const destination = await other.json() as { id: string }
      await page.goto(`/${workspaceKey}/team/${team.key}/all`)
      const row = page.locator(`[data-issue-row-id="${issue.id}"]`)
      await row.click({ button: 'right' })
      await page.getByRole('menuitem', { name: /^Labels/ }).tap()
      const labels = page.getByRole('menu', { name: 'Labels', exact: true })
      await fitsPhone(labels, viewport)
      await shot(page, 'issue-labels', viewport.width)
      const choice = labels.getByRole('menuitemcheckbox').first()
      const labelMutation = page.waitForResponse(response => response.request().method() === 'PATCH' && response.url().includes(`/api/issues/${issue.id}`))
      await choice.tap()
      expect((await labelMutation).ok()).toBe(true)
      await expect(choice).toHaveAttribute('aria-checked', 'true')
      await labels.getByRole('button', { name: 'Back', exact: true }).tap()
      await page.keyboard.press('Escape')
      await row.click({ button: 'right' })
      await page.getByRole('menuitem', { name: 'Move', exact: true }).tap()
      const move = page.getByRole('menu', { name: 'Move', exact: true })
      await fitsPhone(move, viewport)
      await move.getByRole('menuitem', { name: 'Team…', exact: true }).tap()
      const teams = page.getByRole('menu', { name: 'Team…', exact: true })
      await fitsPhone(teams, viewport)
      await expect(move).toBeHidden()
      await teams.getByRole('button', { name: 'Back', exact: true }).tap()
      await expect(move).toBeVisible()
      await move.getByRole('menuitem', { name: 'Team…', exact: true }).tap()
      await shot(page, 'issue-move-team', viewport.width)
      const mutation = page.waitForResponse(response => response.request().method() === 'PATCH' && response.url().includes(`/api/issues/${issue.id}`))
      await teams.getByRole('menuitem', { name: 'Phone destination', exact: true }).tap()
      expect((await mutation).ok()).toBe(true)
      const saved = await (await request.get(`/api/issue-records/${issue.id}`, { headers })).json() as Issue
      expect(saved.team.id).toBe(destination.id)
      expect(saved.labels).toHaveLength(1)
    })

    test('document Move to replaces the dropdown and returns to its actions', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const response = await request.post('/api/documents', { headers, data: { title: 'Phone document submenu', content: 'Phone menu check' } })
      expect(response.status()).toBe(201)
      const document = await response.json() as FlowDocument
      await page.goto(`/${workspaceKey}/document/${document.slugId}`)
      await page.getByRole('button', { name: 'Document options', exact: true }).tap()
      const parent = page.getByRole('menu', { name: 'Document options', exact: true })
      await parent.getByRole('menuitem', { name: /^Move to/ }).tap()
      const menu = page.getByRole('menu', { name: 'Move to', exact: true })
      await fitsPhone(menu, viewport)
      await expect(parent).toBeHidden()
      await shot(page, 'document-move-to', viewport.width)
      await menu.getByRole('button', { name: 'Back', exact: true }).tap()
      await expect(parent).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(parent).toHaveCount(0)
    })

    test('project Labels and grouped label values replace the previous level', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const response = await request.post('/api/projects', { headers, data: { name: 'Phone project submenu' } })
      expect(response.status()).toBe(201)
      const project = await response.json() as Project
      const groupResponse = await request.post('/api/label-groups', { headers, data: { name: 'Phone category', resourceType: 'project' } })
      expect(groupResponse.status()).toBe(201)
      const group = await groupResponse.json() as { id: string }
      const labelResponse = await request.post('/api/labels', { headers, data: { name: 'Phone grouped label', color: '#5e6ad2', resourceType: 'project', groupId: group.id } })
      expect(labelResponse.status()).toBe(201)
      const label = await labelResponse.json() as { id: string }
      await page.goto(`/${workspaceKey}/projects/all`)
      await page.getByRole('row').filter({ hasText: 'Phone project submenu' }).click({ button: 'right' })
      const parent = page.getByRole('menu', { name: 'Project actions', exact: true })
      await parent.getByRole('menuitem', { name: /^Labels/ }).tap()
      const menu = page.getByRole('menu', { name: 'Labels', exact: true })
      await fitsPhone(menu, viewport)
      await expect(parent).toBeHidden()
      await menu.getByRole('option', { name: /^Phone category/ }).tap()
      const values = page.locator('.project-label-submenu')
      await fitsPhone(values, viewport)
      await expect(menu).toBeHidden()
      await shot(page, 'project-label-group', viewport.width)
      await values.getByRole('button', { name: 'Back', exact: true }).tap()
      await expect(menu).toBeVisible()
      await menu.getByRole('option', { name: /^Phone category/ }).tap()
      const mutation = page.waitForResponse(response => response.request().method() === 'PATCH' && response.url().includes(`/api/projects/${project.id}`))
      await values.getByRole('option', { name: 'Phone grouped label', exact: true }).tap()
      expect((await mutation).ok()).toBe(true)
      const saved = await (await request.get(`/api/projects/${project.id}`, { headers })).json() as Project
      expect(saved.labelIds).toContain(label.id)
    })
  })
}
