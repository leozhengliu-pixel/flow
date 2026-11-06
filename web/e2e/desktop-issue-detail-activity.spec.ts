import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, Issue } from '../src/types/flow'

test.skip(({ isMobile }) => isMobile, 'Desktop issue activity and menus')
test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark', launchOptions: { args: ['--force-device-scale-factor=2'] } })

test('fits compact project labels, hides redundant child history, and uses plain issue links', async ({ page, request, workspaceKey: key }) => {
  const headers = { 'X-Workspace-Key': key }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const create = async (title: string, parentId?: string) => {
    const res = await request.post('/api/issues', { headers, data: { title, parentId, teamId: data.teams[0].id } })
    expect(res.status()).toBe(201)
    return res.json() as Promise<Issue>
  }
  const issue = await create('[audit] Desktop specimen')
  const child = await create('[audit] Nested specimen')
  expect((await request.post(`/api/issues/${issue.id}/relations`, { headers, data: { type: 'parent_of', relatedIssueId: child.id } })).status()).toBe(201)
  const related = await create('[audit] Relation target')
  expect((await request.post(`/api/issues/${issue.id}/relations`, { headers, data: { type: 'related', relatedIssueId: related.id } })).status()).toBe(201)
  expect((await request.post('/api/projects', { headers, data: { name: 'Project name fits', teamIds: [issue.team.id] } })).status()).toBe(201)
  await create('[audit] Desktop specimen follow-up')
  await page.goto(`/${key}/issue/${issue.identifier}`)
  const activity = page.locator('.issue-activity-timeline')
  await expect(activity).not.toContainText('parent of')
  const link = activity.getByRole('link', { name: `${related.identifier} ${related.title}`, exact: true })
  await expect(link).toHaveClass('activity-relation-link')
  await expect(link.locator('b')).toHaveText(related.identifier)
  await expect(link).toHaveCSS('font-size', '12px')
  await expect(link).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
  await expect(page.locator('.document-actions svg').first()).toHaveCSS('width', '14px')
  await expect(page.locator('.issue-document')).toHaveCSS('padding-bottom', '0px')
  await expect(page.locator('.issue-properties')).toHaveCSS('padding-left', '0px')
  await expect(page.locator('.issue-properties > h3')).toHaveCSS('margin-left', '4px')
  await expect(page.locator('.issue-relation-section').first()).toHaveCSS('margin-left', '4px')
  const composer = page.locator('.issue-document > .composer')
  expect((await composer.boundingBox())!.x).toBeCloseTo((await page.locator('.title-editor').boundingBox())!.x - 8, 1)
  expect((await composer.boundingBox())!.height).toBe(83)
  await expect(page.locator('.title-editor p')).toHaveCSS('letter-spacing', '-0.16px')
  await expect(page.locator('.issue-command-buttons')).toHaveCSS('width', '158.5px')
  await expect(page.locator('.subscriber-avatars')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
  await page.locator('.issue-properties .issue-project-trigger').click()
  const menu = page.getByRole('dialog', { name: 'Change Project' })
  await expect.poll(async () => Math.round((await menu.boundingBox())!.width)).toBe(186)
  await expect(menu).toHaveCSS('text-rendering', 'geometricprecision')
  await expect(menu.locator('.property-command-create span')).toHaveCSS('text-rendering', 'geometricprecision')
  for (const selector of ['.property-command-create span', '.property-command-label']) {
    for (const element of await menu.locator(selector).all()) {
      expect(await element.evaluate(el => el.scrollWidth <= el.clientWidth)).toBeTruthy()
    }
  }
  await menu.getByRole('option', { name: 'Project name fits', exact: true }).click()
  await expect(page.locator('.issue-properties .issue-project-trigger')).toContainText('Project name fits')
  await expect(page.getByRole('region', { name: 'Similar issues', exact: true })).toHaveCount(0)
  await page.goto(`/${key}/issue/${child.identifier}`)
  await expect(page.locator('.issue-parent-context')).toBeVisible()
  await expect(page.locator('.issue-parent-context > a > strong')).toHaveCSS('font-weight', '450')
  await expect(page.locator('.issue-parent-context > a')).toHaveCSS('gap', '4px')
  await expect(page.locator('.issue-parent-context > a > span')).toHaveCSS('font-weight', '500')
  await expect(page.locator('.issue-parent-context > a > span')).toHaveCSS('color', 'lch(100 0 272)')
  await expect(page.locator('.issue-empty-sub-issue-action > svg')).toHaveCSS('width', '14px')
  await expect(page.getByRole('region', { name: 'Similar issues', exact: true })).toHaveCount(0)
})

test('expands event groups without losing comments and posts a reply with desktop targets', async ({ page, request, workspaceKey: key }) => {
  const headers = { 'X-Workspace-Key': key }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const issue = await (await request.post('/api/issues', { headers, data: { title: '[audit] Comment specimen', teamId: data.teams[0].id } })).json() as Issue
  expect((await request.post(`/api/issues/${issue.id}/comments`, { headers, data: { body: '[audit] Root comment' } })).status()).toBe(201)
  for (const change of [{ priority: 1 }, { assigneeId: data.viewer.id }, { title: '[audit] Updated comment specimen' }]) {
    expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: change })).ok()).toBeTruthy()
  }
  await page.goto(`/${key}/issue/${issue.identifier}`)
  const toggle = page.locator('.activity-event-group')
  await expect(toggle).toHaveText(/^Show 3 events/)
  await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await expect(page.locator('.timeline-item.event')).toHaveCount(1)
  await toggle.click()
  await expect(toggle).toHaveAttribute('aria-expanded', 'true')
  await expect(toggle).toHaveText('Show less')
  await expect(page.locator('.timeline-item.event')).toHaveCount(4)
  await toggle.click()
  await expect(page.locator('.timeline-item.event')).toHaveCount(1)
  const card = page.locator('.comment-card').first()
  await expect(page.locator('.activity-event-icon .avatar').first()).toHaveCSS('font-weight', '400')
  await expect(card.locator(':scope > header > .avatar')).toHaveCSS('font-weight', '400')
  await expect(card.locator(':scope > header > .avatar')).toHaveCSS('font-size', '9px')
  await card.hover()
  for (const button of await card.locator('.comment-card__actions button').all()) {
    await expect(button).toHaveCSS('width', '28px')
    await expect(button).toHaveCSS('height', '28px')
    await expect(button).toHaveCSS('padding', '0px 2px')
    await expect(button).toHaveCSS('border-radius', '9999px')
  }
  const reply = page.locator('.comment-thread__reply')
  await expect(reply).toHaveCSS('font-size', '15px')
  await expect(reply).toHaveCSS('font-weight', '450')
  await expect(reply.locator('span').last()).toHaveCSS('letter-spacing', '-0.1px')
  await reply.click()
  await page.getByRole('textbox', { name: 'Leave a reply…' }).fill('[audit] Posted reply')
  await page.locator('.comment-thread__composer').getByRole('button', { name: 'Submit comment' }).click()
  await expect(page.locator('.comment-reply')).toContainText('[audit] Posted reply')
})

test('opens every action submenu and applies desktop date and reminder presets', async ({ page, request, workspaceKey: key }) => {
  const headers = { 'X-Workspace-Key': key }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const issue = await (await request.post('/api/issues', { headers, data: { title: '[audit] Menu specimen', teamId: data.teams[0].id } })).json() as Issue
  const target = await (await request.post('/api/issues', { headers, data: { title: '[audit] Remove target', teamId: data.teams[0].id } })).json() as Issue
  expect((await request.post(`/api/issues/${issue.id}/relations`, { headers, data: { type: 'related', relatedIssueId: target.id } })).status()).toBe(201)
  await page.goto(`/${key}/issue/${issue.identifier}`)
  // "In 3 hours" stops at 18:00 and "This evening" at 17:00, so the list length follows the clock.
  const hour = new Date().getHours(), threeHoursOffered = hour < 15, eveningOffered = hour < 17
  const trigger = page.getByRole('button', { name: 'Issue options', exact: true })
  await trigger.click()
  await expect(trigger).toHaveCSS('background-color', 'rgb(45, 45, 47)')
  await expect(page.locator('.issue-options-popover [data-submenu="release"]')).toHaveCount(0)
  for (const [key, label, count] of [['create', 'Create related', 5], ['mark', 'Mark as', 6], ['remove', 'Remove', 1], ['copy', 'Copy', 8], ['convert', 'Convert to', 3], ['remind', 'Remind me', 5 + Number(threeHoursOffered) + Number(eveningOffered)]] as const) {
    const parent = page.locator(`.issue-options-popover [data-submenu="${key}"]`)
    await parent.hover()
    const menu = page.getByRole('dialog', { name: label, exact: true })
    await expect(menu).toBeVisible()
    await expect(menu.locator('[cmdk-item]')).toHaveCount(count)
    if (key === 'mark') for (const word of await menu.locator('.issue-options-then').all()) await expect(word).toHaveCSS('line-height', '12.1px')
    for (const label of await menu.locator('.issue-options-label').all()) expect(await label.evaluate(el => el.scrollWidth <= el.clientWidth)).toBeTruthy()
    const root = (await page.locator('.issue-options-popover').boundingBox())!
    const box = (await menu.boundingBox())!
    expect(Math.abs(box.x - (root.x + root.width - 2))).toBeLessThanOrEqual(1)
    expect(Math.abs(box.y - ((await parent.boundingBox())!.y - (key === 'remind' ? 43 : 6.5)))).toBeLessThanOrEqual(1)
  }
  const remind = page.getByRole('dialog', { name: 'Remind me', exact: true })
  const reminderRequest = page.waitForRequest(req => req.method() === 'POST' && req.url().includes('/reminders'))
  await remind.getByRole('option', { name: threeHoursOffered ? /^In 3 hours/ : /^Tomorrow/ }).click()
  const reminder = await reminderRequest
  if (threeHoursOffered) expect(Math.abs(Date.parse(reminder.postDataJSON().remindAt) - Date.now() - 3 * 60 * 60 * 1000)).toBeLessThan(10_000)
  await trigger.click()
  await page.locator('.issue-options-popover [data-submenu="due"]').hover()
  const dates = page.locator('.issue-options-due-date')
  await expect(dates.locator('.due-date-command')).toHaveCSS('min-width', '207px')
  await expect(dates.locator('.due-date-shortcut')).toBeHidden()
  await expect(dates.getByRole('option', { name: /^End of this week/ })).toBeVisible()
  const update = page.waitForResponse(res => res.request().method() === 'PATCH' && res.url().endsWith(`/api/issues/${issue.id}`))
  await dates.getByRole('option', { name: /^End of this week/ }).click()
  expect((await update).ok()).toBeTruthy()
  const saved = await (await request.get(`/api/issue-records/${issue.id}`, { headers })).json() as Issue
  expect(saved.dueDate).toMatch(/^\d{4}-\d{2}-\d{2}$/)
})
