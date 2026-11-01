import { expect } from '@playwright/test'
import { test as base } from './workspace-fixture'
import type { BootstrapData, FlowDocument, Issue, Project } from '../src/types/flow'

// A context scale emulates raster density but rounds hairline layout to whole pixels.
const test = base.extend({
  context: async ({ playwright, baseURL }, provide) => {
    const browser = await playwright.chromium.launch({ args: ['--force-device-scale-factor=2', '--window-size=1440,900'] })
    const context = await browser.newContext({ baseURL, viewport: null, colorScheme: 'dark' })
    await provide(context)
    await browser.close()
  },
})

test.skip(({ isMobile }) => isMobile, 'Desktop physical-scale geometry')

test('keeps the empty agent composer and examples in their content-sized grid tracks', async ({ page, workspaceKey }) => {
  await page.route('**/api/agent/status', route => route.fulfill({ json: { enabled: true, model: 'test' } }))
  await page.goto(`/${workspaceKey}/agent`)
  await expect(page.getByText('Get started with some examples')).toBeVisible()
  expect(await page.evaluate(() => [innerWidth, innerHeight, devicePixelRatio])).toEqual([1440, 900, 2])
  const composer = page.locator('[data-agent-composer]')
  const box = (await composer.boundingBox())!
  expect(box).toMatchObject({ x: 482, width: 712, height: 104 })
  expect(Math.abs(box.y - 314.75)).toBeLessThanOrEqual(1)
  const skills = composer.getByRole('button', { name: 'Skills', exact: true })
  expect((await skills.boundingBox())!.width).toBe(74)
  expect((await skills.boundingBox())!.y - box.y).toBe(68)
  await expect(composer).toHaveCSS('row-gap', '8px')
  await expect(composer).toHaveCSS('border-top-width', '0px')
})

test('sizes document sort targets by their labels and keeps the declared column and group spacing', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const created = await request.post('/api/documents', { headers, data: { title: '[audit] Hairline document', teamIds: [data.teams[0].id] } })
  expect(created.status()).toBe(201)
  const document = await created.json() as FlowDocument
  await page.goto(`/${workspaceKey}/team/${data.teams[0].key}/documents`)
  await expect(page.getByText(document.title, { exact: true })).toBeVisible()
  const targets = page.locator('.team-docs-heading')
  const widths = await targets.evaluateAll(els => els.map(el => el.getBoundingClientRect().width))
  for (const [index, width] of [59.45, 71.36, 88.91, 63.83].entries()) expect(Math.abs(widths[index] - width)).toBeLessThanOrEqual(1)
  await expect(page.locator('.team-docs-group')).toHaveCSS('padding-left', '2px')
  const caret = (await page.locator('.team-docs-caret').boundingBox())!
  expect(caret).toMatchObject({ x: 262.5, width: 20, height: 20 })
  expect((await page.locator('.team-docs-group-title').boundingBox())!.x).toBe(312.5)
  expect((await page.locator('.team-docs-date.is-created').boundingBox())!.x).toBe(1021.5)
  const icon = page.getByRole('button', { name: 'Document icon', exact: true })
  expect((await icon.boundingBox())!).toMatchObject({ x: 286.5, width: 24, height: 24 })
  await icon.click()
  await page.getByRole('tabpanel', { name: 'Icons', exact: true }).getByRole('button', { name: 'BookOpen', exact: true }).click()
  await expect(icon.locator('use')).toHaveAttribute('href', '#BookOpen')
  await expect(page).toHaveURL(new RegExp(`/team/${data.teams[0].key}/documents$`))
  await page.getByRole('button', { name: 'Order by Created', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Order by Created', exact: true })).toHaveAttribute('data-active', 'true')
})

test('uses declared desktop settings padding without inflating the emoticon row', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/settings/account/preferences`)
  const rows = page.locator('.personal-section').first().locator('.settings-row')
  await expect(rows.first()).toBeVisible()
  expect(await rows.evaluateAll(els => els.map(el => el.getBoundingClientRect().height))).toEqual([65, 65, 65, 65, 65])
  await expect(page.locator('.settings-content')).toHaveCSS('padding-top', '64px')
  expect((await page.locator('.personal-page-header').boundingBox())!.y).toBe(72.5)
  await page.goto(`/${workspaceKey}/settings/workspace`)
  await expect(page.locator('.settings-section:has(> h3) .settings-row').first()).toHaveCSS('padding-top', '16px')
  await page.goto(`/${workspaceKey}/settings/teams`)
  const teamHeaders = page.locator('.settings-members-columns button')
  await expect(teamHeaders).toHaveCount(6)
  for (const header of await teamHeaders.all()) await expect(header.locator('svg')).toBeVisible()
  await expect(teamHeaders.first()).toHaveCSS('margin-left', '-6.5px')
  await expect(teamHeaders.first()).toHaveCSS('border-top-width', '0.5px')
  await page.goto(`/${workspaceKey}/settings/integrations`)
  await expect(page.locator('.desktop-enabled')).toHaveCSS('margin-top', '48px')
  await expect(page.locator('.desktop-enabled h2')).toHaveCSS('font-size', '11px')
})

test('preserves desktop hairlines, project insets, comment padding and clipboard toast padding', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const created = await request.post('/api/projects', { headers, data: { name: '[audit] Hairline project', teamIds: [data.teams[0].id] } })
  expect(created.status()).toBe(201)
  const project = await created.json() as Project
  await page.goto(`/${workspaceKey}/projects/all`)
  await expect(page.locator('.lp-projects__header')).toHaveCSS('padding-right', '8px')
  await expect(page.locator('.lp-projects__toolbar')).toHaveCSS('padding-left', '8px')
  await expect(page.locator('.lp-projects__header')).toHaveCSS('border-bottom-width', '0.5px')
  await page.getByRole('button', { name: 'Display options', exact: true }).click()
  await expect(page.locator('.lp-projects-display')).toHaveCSS('border-top-width', '0.5px')
  expect((await page.locator('.lp-projects-display__modes').boundingBox())!.y).toBe(109)
  await page.keyboard.press('Escape')
  await page.goto(`/${workspaceKey}/project/${project.slugId}/activity`)
  await expect(page.locator('.project-activity')).toHaveCSS('padding-top', '44px')
  await expect(page.locator('.project-activity__composer > footer')).toHaveCSS('padding-bottom', '12px')
  const response = await request.post('/api/issues', { headers, data: { title: '[audit] Hairline issue', teamId: data.teams[0].id } })
  expect(response.status()).toBe(201)
  const issue = await response.json() as Issue
  await page.goto(`/${workspaceKey}/team/${data.teams[0].key}/all`)
  await expect(page.locator('[data-issue-explorer]')).toHaveAttribute('data-inset', 'tall')
  expect((await page.locator('[data-issue-explorer]').boundingBox())!.height).toBe(856)
  await page.locator(`[data-issue-row-id="${issue.id}"]`).hover()
  await page.getByRole('checkbox', { name: 'Select issue', exact: true }).check()
  await expect(page.locator('[data-my-issues-bulk-bar]')).toHaveCSS('gap', '8px')
  expect((await request.post(`/api/issues/${issue.id}/comments`, { headers, data: { body: '[audit] Comment' } })).ok()).toBeTruthy()
  await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
  await expect(page.locator('.comment-thread .comment-card')).toHaveCSS('padding', '16px')
  await page.getByRole('button', { name: 'Copy issue URL', exact: true }).click()
  await expect(page.locator('.flow-toast')).toHaveCSS('padding', '12px')
  await expect(page.locator('.flow-toast')).toHaveCSS('border-top-width', '0.5px')
})
