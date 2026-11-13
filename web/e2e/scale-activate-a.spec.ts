import { expect, type Page } from '@playwright/test'
import { test } from './workspace-fixture'

async function subscribed(page: Page, route: string) {
  const response = page.waitForResponse(response => new URL(response.url()).pathname === '/api/realtime/events' && response.status() === 200)
  await page.goto(route)
  await response
}

async function capture(page: Page, name: string) {
  const floatingChat = page.locator('[data-agent-panel][data-variant="floating"]')
  if (await floatingChat.isVisible()) {
    await floatingChat.getByRole('button', { name: 'Close chat', exact: true }).click()
  }
  await expect(floatingChat).toBeHidden()
  await page.evaluate(() => document.fonts.ready)
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
  await expect(floatingChat).toBeHidden()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  if (process.env.SCALE_ACTIVATE_A_SHOTS) await page.screenshot({ path: `${process.env.SCALE_ACTIVATE_A_SHOTS}/${name}.png`, animations: 'disabled', scale: 'css' })
}

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  for (const theme of ['Light', 'Dark']) {
    test.describe(`scale-activate-a ${viewport.width} ${theme}`, () => {
      test.use({ viewport, isMobile: viewport.width < 500, hasTouch: viewport.width < 500, workspaceNamePrefix: 'Activation' })

      test('issues, projects, documents, inbox and Agent retain their business reads and realtime updates', async ({ page, request, workspaceKey }) => {
        test.setTimeout(90_000)
        const headers = { 'X-Workspace-Key': workspaceKey }
        const prefix = `${viewport.width}-${theme.toLowerCase()}`
        const settings = await request.patch('/api/account/settings', { data: { language: 'en-US', interfaceTheme: theme } })
        expect(settings.ok()).toBe(true)
        const core = await (await request.get('/api/bootstrap', { headers })).json()
        const issueResponse = await request.post('/api/issues', { headers, data: { title: 'Activation issue', teamId: core.teams[0].id } })
        expect(issueResponse.status()).toBe(201)
        const issue = await issueResponse.json()
        await subscribed(page, `/${workspaceKey}/issue/${issue.identifier}`)
        const title = page.locator('.issue-title-field .title-editor[contenteditable="true"]')
        await expect(title).toHaveText('Activation issue')
        expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: { title: 'Activation issue updated' } })).ok()).toBe(true)
        await expect(title).toHaveText('Activation issue updated')
        const cacheKey = `flow:navigation:${core.viewer.id}:${workspaceKey}`
        await expect.poll(() => page.evaluate(key => Boolean(localStorage.getItem(key)), cacheKey)).toBe(true)
        await capture(page, `${prefix}-issues`)

        const projectResponse = await request.post('/api/projects', { headers, data: { name: 'Activation project', teamIds: [core.teams[0].id] } })
        expect(projectResponse.status()).toBe(201)
        const project = await projectResponse.json()
        await subscribed(page, `/${workspaceKey}/project/${project.slugId}`)
        await expect(page.locator('main')).toContainText('Activation project')
        expect((await request.patch(`/api/projects/${project.id}`, { headers, data: { name: 'Activation project updated' } })).ok()).toBe(true)
        await expect(page.locator('main')).toContainText('Activation project updated')
        if (viewport.width < 500) {
          const details = page.locator('.project-detail-page__toolbar-button[aria-pressed="true"]').last()
          await details.click()
          await expect(page.locator('.project-details-sidebar')).toBeHidden()
        }
        await capture(page, `${prefix}-projects`)
        await page.goto(`/${workspaceKey}/project/${project.slugId}/activity`)
        await page.locator('.project-activity__composer div[role="tablist"] button').nth(1).click()
        await page.getByRole('button', { name: 'Write with Agent', exact: true }).click()
        const conversation = page.getByRole('log', { name: 'Agent conversation', exact: true })
        await expect(conversation).toHaveCount(1)
        await expect(conversation).toContainText('Verified', { timeout: 30_000 })
        await expect(conversation).toHaveCount(1)
        if (process.env.SCALE_ACTIVATE_A_ECHO_SHOTS) await page.screenshot({ path: `${process.env.SCALE_ACTIVATE_A_ECHO_SHOTS}/${prefix}-project-agent.png`, animations: 'disabled', scale: 'css' })
        await page.getByRole('button', { name: 'Close chat', exact: true }).click()
        await expect(conversation).toHaveCount(0)

        const documentResponse = await request.post('/api/documents', { headers, data: { title: 'Activation document', content: 'Saved activation document body.', teamIds: [core.teams[0].id] } })
        expect(documentResponse.status()).toBe(201)
        const document = await documentResponse.json()
        await subscribed(page, `/${workspaceKey}/document/${document.slugId}`)
        await expect(page.locator('.document-page')).toContainText('Saved activation document body.')
        expect((await request.patch(`/api/documents/${document.id}`, { headers, data: { title: 'Activation document updated' } })).ok()).toBe(true)
        await expect(page.getByRole('textbox', { name: 'Document title' })).toHaveValue('Activation document updated')
        await capture(page, `${prefix}-documents`)

        expect((await request.post(`/api/issues/${issue.id}/reminders`, { headers, data: { remindAt: new Date(Date.now() + 300).toISOString() } })).status()).toBe(201)
        await page.goto(`/${workspaceKey}/inbox`)
        await expect(page.locator('.flow-inbox-row').filter({ hasText: issue.identifier })).toBeVisible({ timeout: 15_000 })
        await capture(page, `${prefix}-inbox`)

        const chatResponse = await request.post('/api/agent/sessions', { headers, data: { message: 'List one issue in this workspace.', location: 'page' } })
        expect(chatResponse.status()).toBe(201)
        const chat = await chatResponse.json()
        await page.goto(`/${workspaceKey}/agent/${chat.slugId}`)
        await expect(page.locator('main')).toContainText('Verified', { timeout: 30_000 })
        await capture(page, `${prefix}-agent`)
        await page.goto(`/${workspaceKey}/document/${document.slugId}`)
        await expect(page.locator('.document-page')).toContainText('Saved activation document body.')
        await page.goto(`/${workspaceKey}/team/${core.teams[0].key}/all`)
        await expect(page.locator('main')).toContainText('Activation issue updated')
        await page.goto(`/${workspaceKey}/projects/all`)
        await expect(page.locator('main')).toContainText('Activation project updated')
        await page.goto(`/${workspaceKey}/team/${core.teams[0].key}/documents`)
        await expect(page.locator('main')).toContainText('Activation document updated')
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
        let oldServerRequests = 0
        await page.route(url => url.pathname === '/api/sync/manifest', async route => {
          oldServerRequests++
          await route.fulfill({ status: 404, json: {} })
        })
        await page.goto(`/${workspaceKey}/document/${document.slugId}`)
        await expect(page.locator('.document-page')).toContainText('Saved activation document body.')
        if (process.env.SCALE_ACTIVATE_A_MODE === 'on') expect(oldServerRequests).toBeGreaterThan(0)
      })
    })
  }
}
