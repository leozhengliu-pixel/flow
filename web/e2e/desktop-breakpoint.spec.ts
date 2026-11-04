import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({ colorScheme: 'dark', reducedMotion: 'reduce' })

test('uses the same boundary in the HTML shell before JavaScript loads', async ({ browser, workspaceKey }) => {
  const context = await browser.newContext({ javaScriptEnabled: false })
  const page = await context.newPage()
  try {
    for (const width of [880, 881, 1024]) {
      await page.setViewportSize({ width, height: 900 })
      await page.goto(`/${workspaceKey}/my-issues/assigned`)
      const geometry = await page.locator('.flow-startup').evaluate(element => ({
        rail: element.querySelector('.flow-startup__rail')!.getBoundingClientRect().width,
        cardX: element.querySelector('.flow-startup__card')!.getBoundingClientRect().x,
      }))
      expect(geometry).toEqual({ rail: width <= 880 ? 0 : 244, cardX: width <= 880 ? 0 : 244 })
    }
  } finally { await context.close() }
})

test('docks every desktop surface at 881px and keeps the drawer below the boundary', async ({ page, request, workspaceKey }) => {
  test.setTimeout(120_000)
  const headers = { 'X-Workspace-Key': workspaceKey }
  const bootstrap = await (await request.get('/api/bootstrap', { headers })).json()
  const team = bootstrap.teams[0]
  const issueResponse = await request.post('/api/issues', { headers, data: { title: 'Breakpoint issue', teamId: team.id } })
  expect(issueResponse.status()).toBe(201)
  const issue = await issueResponse.json()
  const projectResponse = await request.post('/api/projects', { headers, data: { name: 'Breakpoint project', teamIds: [team.id] } })
  expect(projectResponse.status()).toBe(201)
  const project = await projectResponse.json()
  const documentResponse = await request.post('/api/documents', { headers, data: { title: 'Breakpoint document', content: 'Layout verification' } })
  expect(documentResponse.status()).toBe(201)
  const document = await documentResponse.json()
  const routes = ['my-issues/assigned', `team/${team.key}/all`, `issue/${issue.identifier}`, 'projects/all', `project/${project.slugId}/overview`, 'inbox', `document/${document.slugId}`, `team/${team.key}/documents`]

  for (const width of [881, 900, 1000, 1024, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    for (const route of routes) {
      await page.goto(`/${workspaceKey}/${route}`)
      await expect(page.locator('html')).toHaveAttribute('data-sidebar-mode', 'expanded')
      await expect(page.locator('.sidebar-shell')).toHaveCount(0)
      if (route.startsWith('issue/')) await expect(page.locator('.issue-view .title-editor')).toBeVisible()
      await expect(page.locator('.sidebar-layout > .sidebar')).toBeVisible()
      await expect.poll(() => page.locator('.app > main, .app > .flow-framed-workspace').first().evaluate(node => node.getBoundingClientRect().x)).toBe(244)
      await expect(page.locator('main [data-sidebar-trigger]').first()).toBeHidden()
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    }
    await page.goto(`/${workspaceKey}/settings/account/preferences`)
    await expect(page.locator('.settings-sidebar')).toBeVisible()
    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await page.locator('.sidebar-compose').click()
    await expect(page.getByRole('dialog', { name: 'New issue', exact: true })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog', { name: 'New issue', exact: true })).toHaveCount(0)
    await page.locator('[data-content-view-title]').click()
    await page.keyboard.press('ControlOrMeta+k')
    await expect(page.locator('.command-dialog')).toBeVisible()
    await page.keyboard.press('Escape')
  }

  for (const width of [880, 390, 375, 881]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await expect(page.locator('html')).toHaveAttribute('data-sidebar-mode', width <= 880 ? 'compact' : 'expanded')
    await expect(page.locator('[data-my-issues-surface]')).toBeVisible()
    await expect(page.locator('.sidebar-shell')).toHaveCount(0)
    if (width <= 880) {
      const trigger = page.locator('main [data-sidebar-trigger]').first()
      await expect(trigger).toBeVisible()
      await trigger.click()
      await expect(page.locator('html')).toHaveAttribute('data-sidebar-floating', 'true')
      await page.locator('.sidebar-scrim.open').click({ position: { x: width - 5, y: 300 } })
      await expect(page.locator('html')).toHaveAttribute('data-sidebar-floating', 'false')
    }
  }
})
