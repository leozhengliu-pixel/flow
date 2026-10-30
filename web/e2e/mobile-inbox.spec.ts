import { expect, test } from '@playwright/test'

test.describe('mobile inbox list', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })

  test('shows the phone top bar and notification rows', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-inbox-${testInfo.project.name}-${testInfo.retry}`
    const workspaceResponse = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: 'Mobile inbox parity', urlKey: workspaceKey, region: 'us' },
    })
    expect(workspaceResponse.status()).toBe(201)
    const workspace = await workspaceResponse.json() as { teams?: Array<{ id: string }> }
    const teamId = workspace.teams?.[0]?.id
    expect(teamId).toBeTruthy()
    const headers = { 'X-Flow-Workspace': workspaceKey }

    const createIssue = async (title: string, stateId: string) => {
      const response = await request.post('http://127.0.0.1:4180/api/issues', {
        headers,
        data: { title, description: 'Mobile inbox row', teamId, stateId },
      })
      expect(response.status()).toBe(201)
      return response.json() as Promise<{ id: string; identifier: string }>
    }
    const remind = async (issueId: string) => {
      const response = await request.post(`http://127.0.0.1:4180/api/issues/${issueId}/reminders`, {
        headers,
        data: { remindAt: new Date(Date.now() + 500).toISOString() },
      })
      expect(response.status()).toBe(201)
      return response.json() as Promise<{ id: string }>
    }

    const unreadIssue = await createIssue('[mobile] First unread issue', 'state_progress')
    const readIssue = await createIssue('[mobile] Second read issue', 'state_todo')
    const readReminder = await remind(readIssue.id)
    await page.waitForTimeout(900)
    const readResponse = await request.patch(`http://127.0.0.1:4180/api/notifications/${readReminder.id}`, { headers, data: { read: true } })
    expect(readResponse.status()).toBe(200)
    await remind(unreadIssue.id)
    await page.waitForTimeout(900)

    await page.goto(`/${workspaceKey}/inbox`)
    const header = page.locator('header.flow-inbox__header')
    await expect(header).toBeVisible()
    expect((await header.boundingBox())?.height).toBe(44)

    const rows = page.locator('.flow-inbox-notification-list > [role="listitem"] .flow-inbox-row')
    await expect(rows).toHaveCount(2)
    expect((await rows.nth(0).boundingBox())?.height).toBe(55)
    expect((await rows.nth(1).boundingBox())?.height).toBe(55)
    await expect(rows.nth(0).locator('.flow-inbox-row__identifier')).toHaveText(unreadIssue.identifier)
    await expect(rows.nth(1).locator('.flow-inbox-row__identifier')).toHaveText(readIssue.identifier)
    expect((await rows.nth(0).locator('.flow-inbox-row__unread-dot').boundingBox())?.width).toBe(8)
    expect((await rows.nth(1).locator('.flow-inbox-row__unread-dot').boundingBox())?.width).toBe(0)
    expect((await rows.nth(0).locator('.flow-inbox-row__avatar-frame').boundingBox())?.width).toBe(32)
    await expect(rows.nth(0).locator('.flow-inbox-row__identifier')).toHaveCSS('font-weight', '500')
    await expect(rows.nth(1).locator('.flow-inbox-row__identifier')).toHaveCSS('font-weight', '450')

    const toolbar = page.locator('.flow-inbox__header-toolbar-group')
    expect((await toolbar.boundingBox())?.width).toBe(108)
    await expect(toolbar).toHaveCSS('gap', '4px')
    await expect(page.locator('.flow-inbox__header-controls')).toHaveCSS('gap', '6px')

    const geometry = await page.evaluate(() => ({ width: innerWidth, scrollWidth: document.documentElement.scrollWidth }))
    expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.width)

    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/inbox-list-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })
})
