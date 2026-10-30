import { createHmac } from 'node:crypto'
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

  test('opens the phone notification actions sheet', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-inbox-actions-${testInfo.project.name}-${testInfo.retry}`
    const workspaceResponse = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: '[mobile] Inbox actions', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(workspaceResponse.status()).toBe(201)
    const workspace = await workspaceResponse.json() as { teams?: Array<{ id: string }> }
    const teamId = workspace.teams?.[0]?.id
    expect(teamId).toBeTruthy()
    const headers = { 'X-Workspace-Key': workspaceKey }

    for (const title of ['[mobile] Actions notification one', '[mobile] Actions notification two']) {
      const issue = await request.post('http://127.0.0.1:4180/api/issues', { headers, data: { title, description: 'Mobile inbox action row', teamId, stateId: 'state_todo' } })
      expect(issue.status()).toBe(201)
      const issueBody = await issue.json() as { id: string }
      const reminder = await request.post(`http://127.0.0.1:4180/api/issues/${issueBody.id}/reminders`, { headers, data: { remindAt: new Date(Date.now() + 300).toISOString() } })
      expect(reminder.status()).toBe(201)
    }
    await page.waitForTimeout(900)

    await page.goto(`/${workspaceKey}/inbox`)
    await page.locator('.flow-inbox__header button[aria-label="Notification actions"]').click()
    await page.waitForTimeout(400)

    const near = (actual: number | undefined, expected: number) => expect(Math.abs((actual ?? Number.NaN) - expected)).toBeLessThanOrEqual(1)
    const surface = page.locator('.phone-menu-surface.inbox-actions-phone-menu')
    await expect(surface).toBeVisible()
    near((await surface.boundingBox())?.x, 16)
    near((await surface.boundingBox())?.y, 110)
    near((await surface.boundingBox())?.width, 358)
    near((await surface.boundingBox())?.height, 254)

    const search = surface.locator('input')
    near((await search.boundingBox())?.x, 22.5)
    near((await search.boundingBox())?.y, 116.5)
    near((await search.boundingBox())?.width, 346)
    near((await search.boundingBox())?.height, 40)

    const rows = surface.locator('.inbox-actions-phone-menu__row')
    await expect(rows).toHaveCount(4)
    await expect(rows.locator('.inbox-actions-phone-menu__label')).toHaveText(['Delete all', 'Delete all read', 'Delete all completed', 'Go to settings'])
    near((await rows.nth(0).boundingBox())?.y, 162.5)
    near((await rows.nth(0).boundingBox())?.height, 46)
    near((await rows.nth(0).boundingBox())?.width, 346)
    near((await rows.nth(0).locator('.inbox-actions-phone-menu__label').boundingBox())?.x, 58.5)
    near((await rows.nth(3).boundingBox())?.y, 312.5)

    const divider = surface.locator('.inbox-actions-phone-menu__separator')
    near((await divider.boundingBox())?.x, 16.5)
    near((await divider.boundingBox())?.y, 300.5)
    near((await divider.boundingBox())?.width, 358)
    near((await divider.boundingBox())?.height, 12)

    await expect(surface.getByRole('menuitem', { name: 'Go to settings' })).toBeVisible()

    const geometry = await page.evaluate(() => ({ width: innerWidth, scrollWidth: document.documentElement.scrollWidth }))
    expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.width)

    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/inbox-actions-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })

  test('shows the phone Pulse feed with one initiative update card', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-inbox-pulse-${testInfo.project.name}-${testInfo.retry}`
    const workspaceResponse = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: '[mobile] Pulse feed', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(workspaceResponse.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const settings = await request.patch('http://127.0.0.1:4180/api/account/settings', { headers, data: { pulseWelcomeDismissed: true } })
    expect(settings.status()).toBe(200)
    const initiative = await request.post('http://127.0.0.1:4180/api/initiatives', { headers, data: { name: '[mobile] test123' } })
    expect(initiative.status()).toBe(201)
    const initiativeBody = await initiative.json() as { id: string }
    const update = await request.post(`http://127.0.0.1:4180/api/initiatives/${initiativeBody.id}/updates`, { headers, data: { body: '[mobile] test', health: 'onTrack' } })
    expect(update.status()).toBe(201)

    await page.goto(`/${workspaceKey}/pulse/following`)
    const header = page.locator('header.pulse-header')
    await expect(header).toBeVisible()
    expect((await header.boundingBox())?.height).toBe(87.5)
    await expect(header).toHaveCSS('background-color', 'lch(5.52 0.4 272)')

    const near = (actual: number | undefined, expected: number) => expect(Math.abs((actual ?? Number.NaN) - expected)).toBeLessThanOrEqual(1)
    const menu = page.locator('.pulse-mobile-menu')
    expect((await menu.boundingBox())?.width).toBe(32)
    expect((await menu.boundingBox())?.height).toBe(32)
    near((await menu.boundingBox())?.x, 8)
    const title = page.locator('.pulse-header h2')
    near((await title.boundingBox())?.x, 52)
    const subscription = page.locator('.pulse-header button[aria-label="Subscription"]')
    expect((await subscription.boundingBox())?.width).toBe(32)

    const tabs = page.locator('.pulse-view-tabs a.pulse-tab')
    await expect(tabs).toHaveCount(3)
    expect((await tabs.nth(0).boundingBox())?.height).toBe(32)
    await expect(tabs.nth(0)).toHaveCSS('background-color', 'lch(16.706 0.979 272)')
    near((await tabs.nth(2).boundingBox())?.x, 141.44)
    const addView = page.locator('.pulse-add-view')
    expect((await addView.boundingBox())?.width).toBe(32)
    near((await addView.boundingBox())?.x, 206.37)

    const section = page.locator('.pulse-group-header > span')
    near((await section.boundingBox())?.y, 135.5)
    const card = page.locator('.pulse-feed-item > .pulse-post-card')
    near((await card.boundingBox())?.y, 175)
    near((await card.boundingBox())?.width, 374)
    near((await card.boundingBox())?.height, 134.5)

    const cardTitle = page.locator('.pulse-post-title')
    near((await cardTitle.boundingBox())?.y, 175)
    await expect(cardTitle).toHaveCSS('font-size', '18px')
    await expect(cardTitle).toHaveCSS('font-weight', '550')
    const badge = page.locator('.pulse-post-health > span')
    near((await badge.boundingBox())?.x, 32)
    await expect(badge).toHaveText('Initiative on track')
    await expect(badge).toHaveCSS('color', 'lch(67.2 64.37 141.95)')
    const avatar = page.locator('.pulse-post-card .pulse-byline-avatar')
    expect((await avatar.boundingBox())?.width).toBe(18)
    expect((await avatar.boundingBox())?.height).toBe(18)
    near((await avatar.boundingBox())?.x, 149.64)
    const author = page.locator('.pulse-post-author')
    near((await author.boundingBox())?.y, 204.75)
    const time = page.locator('.pulse-post-byline time')
    near((await time.boundingBox())?.y, 204.75)
    const body = page.locator('.pulse-post-body')
    near((await body.boundingBox())?.y, 237.5)
    await expect(body).toHaveCSS('font-size', '15px')
    await expect(body).toHaveCSS('font-weight', '450')
    await expect(body).toHaveCSS('line-height', '24px')

    const comment = page.locator('.pulse-post-footer > .pulse-footer-button')
    near((await comment.boundingBox())?.x, 8)
    near((await comment.boundingBox())?.y, 277.5)
    near((await comment.boundingBox())?.width, 37)
    expect((await comment.boundingBox())?.height).toBe(28)
    const reaction = page.locator('.pulse-post-reactions .pulse-footer-button')
    near((await reaction.boundingBox())?.x, 53)
    expect((await reaction.boundingBox())?.width).toBe(32)
    expect((await reaction.boundingBox())?.height).toBe(32)

    const geometry = await page.evaluate(() => ({ width: innerWidth, scrollWidth: document.documentElement.scrollWidth }))
    expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.width)

    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/pulse-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })
  test('shows the phone reviews header, tab counts and empty state', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-inbox-reviews-list-${testInfo.project.name}-${testInfo.retry}`
    const workspaceResponse = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: '[mobile] Reviews list', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(workspaceResponse.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const secret = `mobile-reviews-${workspaceKey}`
    const integration = await request.put('http://127.0.0.1:4180/api/integrations/github', {
      headers,
      data: { name: '[mobile] GitHub', config: { organization: 'mobile-e2e', webhookSecret: secret, codeAccess: 'true' }, scopes: ['repo'] },
    })
    expect(integration.status()).toBe(200)

    const webhook = async (payload: Record<string, unknown>, delivery: string) => {
      const body = JSON.stringify(payload)
      const signature = 'sha256=' + createHmac('sha256', secret).update(body).digest('hex')
      const response = await request.post('http://127.0.0.1:4180/api/integrations/github/webhook', {
        headers: { ...headers, 'Content-Type': 'application/json', 'X-GitHub-Event': 'pull_request', 'X-GitHub-Delivery': delivery, 'X-Hub-Signature-256': signature },
        data: body,
      })
      expect(response.status()).toBe(202)
    }
    for (const number of [101, 102]) {
      const pullRequest = { number, id: 9000 + number, title: `[mobile] Review ${number}`, body: '', html_url: `https://github.com/mobile-e2e/repo/pull/${number}`, state: 'open', user: { login: 'flowuser' }, base: { ref: 'main' }, head: { ref: `mobile/review-${number}`, sha: 'abc' }, additions: 3, deletions: 1, commits: 1 }
      const event = { number, pull_request: pullRequest, repository: { full_name: 'mobile-e2e/repo' }, sender: { login: 'flowuser' } }
      await webhook({ ...event, action: 'opened' }, `${workspaceKey}-open-${number}`)
      await webhook({ ...event, action: 'closed', pull_request: { ...pullRequest, state: 'closed', merged: true } }, `${workspaceKey}-closed-${number}`)
    }

    await page.goto(`/${workspaceKey}/reviews`)
    const near = (actual: number | undefined, expected: number) => expect(Math.abs((actual ?? Number.NaN) - expected)).toBeLessThanOrEqual(1)

    const header = page.locator('.reviews-header')
    await expect(header).toBeVisible()
    near((await header.boundingBox())?.height, 87.5)
    const menu = page.locator('.reviews-topbar [data-sidebar-trigger]')
    near((await menu.boundingBox())?.x, 8)
    expect((await menu.boundingBox())?.width).toBe(32)
    near((await page.locator('.reviews-topbar h1').boundingBox())?.x, 52)
    near((await page.locator('.reviews-topbar button[aria-label="Add filter"]').boundingBox())?.x, 314)
    near((await page.locator('.reviews-topbar button[aria-label="Display options"]').boundingBox())?.x, 350)

    const created = page.locator('.reviews-tabs a:nth-of-type(2)')
    const toReview = page.locator('.reviews-tabs a:nth-of-type(1)')
    near((await created.boundingBox())?.x, 8)
    near((await toReview.boundingBox())?.x, 92.75)
    expect((await created.boundingBox())?.height).toBe(32)
    await expect(created).toHaveCSS('background-color', 'lch(10.149 0.593 272)')
    await expect(toReview).toHaveCSS('background-color', 'lch(16.706 0.979 272)')
    await expect(toReview.locator('.reviews-tab-label--phone')).toHaveText('To review')
    await expect(created.locator('.reviews-tab-label--phone')).toHaveText('Created')
    await expect(created.locator('.reviews-tab-count')).toHaveText('2')
    near((await created.locator('.reviews-tab-count').boundingBox())?.x, 69.63)
    expect((await created.locator('.reviews-tab-count').boundingBox())?.height).toBe(13)

    // Hide the closed (merged) reviews of this view, as the reference account does, so the To review tab is empty.
    await page.locator('.reviews-topbar button[aria-label="Display options"]').click()
    await page.locator('.review-list-options .select-control[aria-label="Closed reviews"]').click()
    await page.locator('[role="option"]:has-text("None")').click()
    await page.locator('.reviews-topbar h1').click()

    const illustration = page.locator('.reviews-empty-illustration svg')
    await expect(illustration).toBeVisible()
    near((await illustration.boundingBox())?.x, 130)
    near((await illustration.boundingBox())?.y, 380.5)
    near((await illustration.boundingBox())?.width, 130)
    near((await illustration.boundingBox())?.height, 131)
    near((await page.locator('.reviews-empty-illustration').boundingBox())?.y, 371)
    const emptyText = page.locator('.reviews-empty-text')
    await expect(emptyText).toHaveText('Nothing to review')
    near((await emptyText.boundingBox())?.y, 545)
    await expect(emptyText).toHaveCSS('font-size', '13px')
    await expect(emptyText).toHaveCSS('font-weight', '500')

    const geometry = await page.evaluate(() => ({ width: innerWidth, scrollWidth: document.documentElement.scrollWidth }))
    expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.width)

    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/reviews-list-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })

  test('groups the phone Created reviews by Ready to merge and Draft', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-inbox-reviews-created-${testInfo.project.name}-${testInfo.retry}`
    const workspaceResponse = await request.post('http://127.0.0.1:4180/api/workspaces', {
      data: { name: '[mobile] Reviews created', urlKey: workspaceKey, region: 'us', tutorial: false },
    })
    expect(workspaceResponse.status()).toBe(201)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const secret = `mobile-reviews-created-${workspaceKey}`
    const integration = await request.put('http://127.0.0.1:4180/api/integrations/github', {
      headers,
      data: { name: '[mobile] GitHub', config: { organization: 'mobile-e2e', webhookSecret: secret, codeAccess: 'true' }, scopes: ['repo'] },
    })
    expect(integration.status()).toBe(200)

    const webhook = async (number: number, delivery: string) => {
      const pullRequest = { number, id: 9100 + number, title: `[mobile] Review ${number}`, body: '', html_url: `https://github.com/mobile-e2e/repo/pull/${number}`, state: 'open', user: { login: 'flowuser' }, base: { ref: 'main' }, head: { ref: `mobile/review-${number}`, sha: 'abc' }, additions: 3, deletions: 1, commits: 1 }
      const body = JSON.stringify({ action: 'opened', number, pull_request: pullRequest, repository: { full_name: 'mobile-e2e/repo' }, sender: { login: 'flowuser' } })
      const signature = 'sha256=' + createHmac('sha256', secret).update(body).digest('hex')
      const response = await request.post('http://127.0.0.1:4180/api/integrations/github/webhook', {
        headers: { ...headers, 'Content-Type': 'application/json', 'X-GitHub-Event': 'pull_request', 'X-GitHub-Delivery': delivery, 'X-Hub-Signature-256': signature },
        data: body,
      })
      expect(response.status()).toBe(202)
      return (await response.json() as { reviewId: string }).reviewId
    }
    const readyOne = await webhook(201, `${workspaceKey}-open-201`)
    const readyTwo = await webhook(202, `${workspaceKey}-open-202`)
    const draft = await webhook(203, `${workspaceKey}-open-203`)
    for (const id of [readyOne, readyTwo]) {
      const approved = await request.patch(`http://127.0.0.1:4180/api/reviews/${id}`, { headers, data: { status: 'approved' } })
      expect(approved.status()).toBe(200)
    }
    const drafted = await request.patch(`http://127.0.0.1:4180/api/reviews/${draft}`, { headers, data: { draft: true } })
    expect(drafted.status()).toBe(200)

    await page.goto(`/${workspaceKey}/reviews/created`)
    const near = (actual: number | undefined, expected: number) => expect(Math.abs((actual ?? Number.NaN) - expected)).toBeLessThanOrEqual(1)

    // The reference account groups the Created view by focus; pick it the way a user does.
    await page.locator('.reviews-topbar button[aria-label="Display options"]').click()
    await page.locator('.review-list-options .select-control[aria-label="Grouping"]').click()
    await page.locator('[role="option"]:has-text("Focus")').click()
    await page.locator('.reviews-topbar h1').click()

    const created = page.locator('.reviews-tabs a:nth-of-type(2)')
    await expect(created.locator('.reviews-tab-label--phone')).toHaveText('Created')
    await expect(created).toHaveCSS('background-color', 'lch(16.706 0.979 272)')
    const toReview = page.locator('.reviews-tabs a:nth-of-type(1)')
    near((await toReview.boundingBox())?.width, 75.29)
    await expect(toReview.locator('.reviews-tab-count')).toHaveCount(0)

    const sections = page.locator('.review-groups > section')
    await expect(sections).toHaveCount(2)
    const readyBar = sections.nth(0).locator('.review-group-heading')
    near((await readyBar.boundingBox())?.x, 8)
    near((await readyBar.boundingBox())?.y, 87.5)
    near((await readyBar.boundingBox())?.width, 374)
    near((await readyBar.boundingBox())?.height, 28)
    await expect(readyBar).toHaveCSS('background-color', 'lch(9.232 0.85 272)')
    await expect(readyBar.locator(':scope > span')).toHaveText('Ready to merge')
    await expect(readyBar.locator(':scope > small')).toHaveText('2')
    near((await readyBar.locator(':scope > small').boundingBox())?.x, 113.2)
    const chevron = readyBar.locator('.review-group-chevron')
    near((await chevron.boundingBox())?.x, 120.97)
    expect((await chevron.boundingBox())?.width).toBe(16)

    const rows = sections.nth(0).locator('.review-row')
    await expect(rows).toHaveCount(2)
    const firstRow = rows.nth(0)
    near((await firstRow.boundingBox())?.x, 0)
    near((await firstRow.boundingBox())?.y, 117.5)
    near((await firstRow.boundingBox())?.width, 390)
    near((await firstRow.boundingBox())?.height, 40)
    const icon = firstRow.locator('.review-row-icon--phone')
    near((await icon.boundingBox())?.x, 16)
    near((await icon.boundingBox())?.y, 130.5)
    expect((await icon.boundingBox())?.width).toBe(14)
    await expect(icon.locator('path')).toHaveCount(4)
    near((await firstRow.locator('.review-list-row__title').boundingBox())?.x, 38)
    near((await firstRow.locator('time').boundingBox())?.x, 344)

    const draftBar = sections.nth(1).locator('.review-group-heading')
    near((await draftBar.boundingBox())?.y, 199.5)
    await expect(draftBar.locator(':scope > span')).toHaveText('Draft')
    await expect(draftBar.locator(':scope > small')).toHaveText('1')
    const draftRow = sections.nth(1).locator('.review-row')
    await expect(draftRow).toHaveCount(1)
    near((await draftRow.boundingBox())?.y, 229.5)
    await expect(draftRow.locator('.review-row-icon--phone')).toHaveAttribute('data-review-state', 'draft')

    const geometry = await page.evaluate(() => ({ width: innerWidth, scrollWidth: document.documentElement.scrollWidth }))
    expect(geometry.scrollWidth).toBeLessThanOrEqual(geometry.width)

    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/reviews-created-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })

})
