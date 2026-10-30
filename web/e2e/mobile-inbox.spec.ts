import { expect, test } from '@playwright/test'

test.describe('mobile inbox list', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('shows the phone top bar and notification rows', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-inbox-${testInfo.project.name}-${testInfo.retry}`
    const workspaceResponse = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: 'Mobile inbox parity', urlKey: workspaceKey, region: 'us' },
    })
    expect(workspaceResponse.status()).toBe(201)
    const workspace = await workspaceResponse.json() as { teams?: Array<{ id: string }> }
    const teamId = workspace.teams?.[0]?.id
    expect(teamId).toBeTruthy()
    const headers = { 'X-Workspace-Key': workspaceKey }

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

  test('opens a notification as the phone issue page with notification actions', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-inbox-notification-${testInfo.project.name}-${testInfo.retry}`
    const workspaceResponse = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: '[mobile] Inbox notification', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(workspaceResponse.status()).toBe(201)
    const workspace = await workspaceResponse.json() as { teams?: Array<{ id: string }> }
    const teamId = workspace.teams?.[0]?.id
    expect(teamId).toBeTruthy()
    const headers = { 'X-Workspace-Key': workspaceKey }
    const createIssue = async (title: string, description: string) => {
      const response = await request.post('http://127.0.0.1:4180/api/issues', { headers, data: { title, description, teamId, stateId: 'state_todo' } })
      expect(response.status()).toBe(201)
      return response.json() as Promise<{ id: string; identifier: string }>
    }
    const related = await createIssue('[mobile] Related issue', '[mobile] Related issue body')
    const issue = await createIssue('[mobile] Inbox notification issue', 'External https://example.com\n\nIssue https://example.com/flow-app12/issue/FLO-1/get-familiar-with-flow\n\nProject https://example.com/app12/projects/all\n\nCustom custom text after')
    const relation = await request.post(`http://127.0.0.1:4180/api/issues/${issue.id}/relations`, { headers, data: { type: 'related', relatedIssueId: related.id } })
    expect(relation.ok()).toBe(true)
    const comment = await request.post(`http://127.0.0.1:4180/api/issues/${issue.id}/comments`, { headers, data: { body: '[mobile] A comment on the issue' } })
    expect(comment.status()).toBe(201)
    const reminder = await request.post(`http://127.0.0.1:4180/api/issues/${issue.id}/reminders`, { headers, data: { remindAt: new Date(Date.now() + 400).toISOString() } })
    expect(reminder.status()).toBe(201)
    await page.waitForTimeout(1200)

    await page.goto(`/${workspaceKey}/inbox`)
    const row = page.locator('.flow-inbox-notification-list > [role="listitem"] [role="link"]').first()
    await expect(row).toBeVisible()
    await row.click()

    const header = page.locator('.flow-inbox-detail__header')
    await expect(header).toBeVisible()
    expect((await header.boundingBox())?.height).toBe(44)
    const menu = page.locator('.flow-inbox-detail__menu')
    await expect(menu).toBeVisible()
    expect((await menu.boundingBox())?.width).toBe(32)
    await expect(page.getByRole('button', { name: 'Add to favorites' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Unsubscribe from issue' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Snooze notification' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Delete notification', exact: true })).toBeVisible()

    for (const name of ['Copy issue URL', 'Copy issue ID', 'Copy branch name']) {
      const button = page.getByRole('button', { name, exact: true })
      await expect(button).toBeVisible()
      expect((await button.boundingBox())?.width).toBe(32)
      expect((await button.boundingBox())?.y).toBeCloseTo(51.5, 0)
    }

    const title = page.locator('.flow-inbox-detail .title-editor p')
    await expect(title).toHaveText('[mobile] Inbox notification issue')
    expect((await title.boundingBox())?.x).toBe(14)
    expect((await title.boundingBox())?.y).toBeCloseTo(89.75, 0)
    await expect(page.locator('.flow-inbox-detail .description-editor p').first()).toContainText('https://example.com')
    await expect(page.locator('.flow-inbox-detail .activity-heading > span')).toHaveText('Activity')
    const divider = page.locator('.flow-inbox-detail__divider')
    await expect(divider).toBeVisible()
    expect((await divider.boundingBox())?.y).toBeCloseTo(531.75, 0)
    const relationLink = page.locator('.flow-inbox-detail .activity-relation-link')
    await expect(relationLink).toContainText(related.identifier)
    await expect(relationLink.locator('[data-agent-entity="issue"]')).toHaveCount(0)
    const properties = page.locator('.flow-inbox-detail .issue-mobile-properties')
    expect((await properties.boundingBox())?.width).toBe(390 - 28)
    expect((await properties.boundingBox())?.y).toBeCloseTo(145.75, 0)

    const geometry = await page.evaluate(() => ({ width: innerWidth, scrollWidth: document.documentElement.scrollWidth }))
    expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.width)

    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/inbox-notification-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })

  test('opens the phone filter sheet with the reference field list', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-inbox-filters-${testInfo.project.name}-${testInfo.retry}`
    const workspaceResponse = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: '[mobile] Inbox filters', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(workspaceResponse.status()).toBe(201)
    const workspace = await workspaceResponse.json() as { teams?: Array<{ id: string }> }
    const teamId = workspace.teams?.[0]?.id
    expect(teamId).toBeTruthy()
    const headers = { 'X-Workspace-Key': workspaceKey }

    const initiative = await request.post('http://127.0.0.1:4180/api/initiatives', { headers, data: { name: '[mobile] Filter initiative', leadTeamId: teamId } })
    expect(initiative.status()).toBe(201)
    for (const title of ['[mobile] Filter notification one', '[mobile] Filter notification two']) {
      const issue = await request.post('http://127.0.0.1:4180/api/issues', { headers, data: { title, description: 'Mobile inbox filter row', teamId, stateId: 'state_todo' } })
      expect(issue.status()).toBe(201)
      const issueBody = await issue.json() as { id: string }
      const reminder = await request.post(`http://127.0.0.1:4180/api/issues/${issueBody.id}/reminders`, { headers, data: { remindAt: new Date(Date.now() + 300).toISOString() } })
      expect(reminder.status()).toBe(201)
    }
    await page.waitForTimeout(900)

    await page.goto(`/${workspaceKey}/inbox`)
    await page.locator('.flow-inbox__header-controls button[aria-label="Add filter"]').click()
    await page.waitForTimeout(400)

    const near = (actual: number | undefined, expected: number) => expect(Math.abs((actual ?? Number.NaN) - expected)).toBeLessThanOrEqual(1)
    const surface = page.locator('.phone-menu-surface.inbox-filter-phone-properties')
    await expect(surface).toBeVisible()
    near((await surface.boundingBox())?.x, 16)
    near((await surface.boundingBox())?.y, 110)
    near((await surface.boundingBox())?.width, 358)
    near((await surface.boundingBox())?.height, 380)

    const search = surface.locator('input')
    near((await search.boundingBox())?.x, 22.5)
    near((await search.boundingBox())?.y, 116.5)
    near((await search.boundingBox())?.width, 346)
    near((await search.boundingBox())?.height, 40)

    const list = surface.locator('[cmdk-list]')
    near((await list.boundingBox())?.x, 22.5)
    near((await list.boundingBox())?.y, 156.5)
    near((await list.boundingBox())?.width, 346)
    near((await list.boundingBox())?.height, 334)

    const items = surface.locator('[cmdk-item]')
    await expect(items).toHaveCount(7)
    await expect(items.locator('span:last-child')).toHaveText(['Notification type', 'From', 'Team', 'Project', 'Initiative', 'Issue priority', 'Issue status type'])
    near((await items.nth(0).boundingBox())?.y, 162.5)
    near((await items.nth(0).boundingBox())?.height, 46)
    await expect(items.nth(0)).toHaveAttribute('data-selected', 'true')
    const selected = await items.nth(0).evaluate(node => ({ background: getComputedStyle(node).backgroundColor, color: getComputedStyle(node.querySelector('span:last-child')!).color }))
    expect(selected.background).toBe('rgba(0, 0, 0, 0)')
    expect(selected.color).toBe('lch(100 0 272)')
    near((await items.nth(0).locator('span:last-child').boundingBox())?.x, 58.5)

    const trigger = page.locator('.flow-inbox__header-controls button[aria-label="Add filter"]')
    near((await trigger.boundingBox())?.width, 32)
    expect((await trigger.evaluate(node => getComputedStyle(node).color))).toBe('lch(100 0 272)')

    const geometry = await page.evaluate(() => ({ width: innerWidth, scrollWidth: document.documentElement.scrollWidth }))
    expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.width)

    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/inbox-filters-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })
})
