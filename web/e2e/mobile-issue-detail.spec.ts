import { expect, test, type Page } from '@playwright/test'
import { mkdirSync, writeFileSync } from 'node:fs'

const userAgent = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1'
test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent, colorScheme: 'dark' })
const shots = process.env.FLOW_MOBILE_SHOTS
let workspaceKey: string
let parent: { id: string; identifier: string }
let child: { id: string; identifier: string }
let related: { id: string; identifier: string }

test.beforeAll(async ({ request }) => {
  workspaceKey = `mobile-issue-detail-${Date.now()}`
  const workspace = await request.post('/api/workspaces', { data: { name: '[mobile] Issue detail', urlKey: workspaceKey, region: 'us' } })
  expect(workspace.status()).toBe(201)
  const headers = { 'X-Workspace-Key': workspaceKey }
  const bootstrap = await request.get('/api/bootstrap', { headers })
  const data = await bootstrap.json()
  const teamId = data.teams[0].id
  const stateId = data.states.find((state: { type: string }) => state.type === 'unstarted').id
  const create = async (title: string, extra = {}) => {
    const response = await request.post('/api/issues', { headers, data: { title, description: '', teamId, stateId, ...extra } })
    expect(response.status()).toBe(201)
    return response.json()
  }
  parent = await create('[mobile] A blocker')
  child = await create('[mobile] D duplicate', { parentId: parent.id })
  related = await create('[mobile] B blocked')
  const relation = await request.post(`/api/issues/${parent.id}/relations`, { headers, data: { type: 'blocks', relatedIssueId: related.id } })
  expect(relation.ok()).toBe(true)
  if (shots) { mkdirSync(shots, { recursive: true }); writeFileSync(`${shots}/fixture.json`, JSON.stringify({ workspaceKey, parent, child, related }, null, 2)) }
})

async function openIssue(page: Page, identifier = parent.identifier) {
  await page.goto(`/${workspaceKey}/issue/${identifier}`)
  await expect(page.getByRole('textbox', { name: 'Issue title', exact: true })).toBeVisible()
  await page.evaluate(async () => {
    await document.fonts.ready
    await new Promise(requestAnimationFrame)
    await new Promise(requestAnimationFrame)
  })
  await expect(page.locator('html')).toHaveAttribute('data-theme','dark')
}

async function noOverflow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  expect(await page.locator('.issue-scroll').evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true)
}

async function geometry(page: Page) {
  return page.evaluate(() => Object.fromEntries(['.issue-header','.title-editor','.issue-mobile-properties','.issue-description-root .description-editor','.document-actions','.flow-sub-issues','.detail-sub-issue-row','.activity-heading','.composer'].map(selector => {
    const element = document.querySelector(selector)!
    const rect = element.getBoundingClientRect(), style = getComputedStyle(element)
    return [selector, { x: rect.x, y: rect.y, width: rect.width, height: rect.height, fontSize: style.fontSize, lineHeight: style.lineHeight, padding: style.padding, gap: style.gap, color: style.color, background: style.backgroundColor, radius: style.borderRadius }]
  })))
}

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test(`issue geometry and navigation at ${viewport.width}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await openIssue(page)
    const values = await geometry(page)
    expect(values['.issue-header'].height).toBeCloseTo(43.5, 0)
    expect(values['.title-editor'].x).toBe(14)
    expect(values['.title-editor'].y).toBeCloseTo(89.75, 0)
    expect(values['.title-editor'].width).toBe(viewport.width - 28)
    expect(values['.title-editor'].fontSize).toBe('24px')
    expect(values['.issue-mobile-properties'].gap).toBe('6px')
    expect(values['.issue-mobile-properties'].height).toBe(viewport.width===390?62:28)
    expect(values['.issue-mobile-properties'].y).toBeCloseTo(145.75, 0)
    const status = page.locator('.issue-mobile-properties .core-property-trigger').first()
    await expect(status).toHaveCSS('height', '28px')
    await expect(status).toHaveCSS('padding', '0px 7.5px 0px 5.5px')
    await expect(status).toHaveCSS('box-shadow', 'none')
    expect((await page.locator('.issue-breadcrumb-current').boundingBox())?.x).toBe(48)
    expect(values['.issue-header'].gap).toBe('0px')
    expect(values['.issue-description-root .description-editor'].radius).toBe('0px')
    // Text metrics vary across browser platforms; the chip must size to its contents without clipping.
    expect(await status.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true)
    expect(values['.issue-description-root .description-editor'].y).toBeCloseTo(viewport.width===390?229.75:195.75, 0)
    expect(values['.issue-description-root .description-editor'].padding).toBe('10px 14px 16px')
    expect(values['.detail-sub-issue-row'].height).toBe(36)
    expect(values['.composer'].x).toBe(6)
    expect(values['.composer'].width).toBe(viewport.width - 12)
    expect(values['.composer'].height).toBeCloseTo(87, 0)
    await expect(page.locator('.issue-properties-rail')).toBeHidden()
    await expect(page.getByRole('button', { name: 'Copy issue URL', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Copy issue ID', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Copy branch name', exact: true })).toBeVisible()
    await noOverflow(page)
    if (shots) {
      writeFileSync(`${shots}/geometry-${viewport.width}.json`, JSON.stringify(values, null, 2))
      await page.screenshot({ path: `${shots}/flow-parent-${viewport.width}.png`, scale: 'css' })
    }
    await page.getByRole('button', { name: 'Collapse sub-issues section' }).click()
    await expect(page.locator('.detail-sub-issue-row')).toHaveCount(0)
    await page.getByRole('button', { name: 'Expand sub-issues section' }).click()
    await page.locator('.detail-sub-issue-row').click()
    await expect(page.getByRole('textbox', { name: 'Issue title', exact: true })).toHaveText('[mobile] D duplicate')
    await page.locator('.issue-parent-context a').click()
    await expect(page.getByRole('textbox', { name: 'Issue title', exact: true })).toHaveText('[mobile] A blocker')
    await page.getByRole('button', { name: 'Open related issue', exact: true }).click()
    const relations = page.getByRole('dialog', { name: 'Related issues', exact: true })
    await expect(relations).toBeVisible()
    await relations.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
    expect((await relations.boundingBox())?.x).toBeCloseTo(16, 0)
    expect((await relations.boundingBox())?.y).toBeCloseTo(viewport.height*0.130332, 0)
    expect((await relations.boundingBox())?.width).toBeCloseTo(viewport.width-32, 0)
    await expect(relations.getByRole('link')).toHaveCount(1)
    if (shots) await page.screenshot({ path: `${shots}/flow-relations-${viewport.width}.png`, scale: 'css' })
    await relations.getByRole('link').click()
    await expect(page.getByRole('textbox', { name: 'Issue title', exact: true })).toHaveText('[mobile] B blocked')
  })
}

test('issue actions are touch-sized and nested actions can return', async ({ page }) => {
  await openIssue(page)
  await page.getByRole('button', { name: 'Issue options', exact: true }).click()
  const menu = page.locator('.issue-options-popover')
  await expect(menu).toBeVisible()
  await menu.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
  await expect.poll(async () => (await menu.boundingBox())?.y).toBeCloseTo(110, 0)
  await expect.poll(async () => (await menu.boundingBox())?.width).toBeCloseTo(358, 0)
  const box = await menu.boundingBox()
  expect(box?.x).toBe(16)
  expect(box?.width).toBe(358)
  expect(box?.height).toBe(450)
  expect((await menu.locator('[cmdk-item]').first().boundingBox())?.height).toBe(46)
  if (shots) await page.screenshot({ path: `${shots}/flow-options-390.png`, scale: 'css' })
  await menu.getByRole('option', { name: 'Create related', exact: true }).click()
  const nested = page.locator('.issue-options-submenu')
  await expect(nested).toBeVisible()
  await nested.getByRole('button', { name: 'Back', exact: true }).click()
  await expect(menu).toBeVisible()
  await menu.getByRole('option', { name: 'Due date', exact: true }).click()
  await expect(page.locator('.issue-options-due-date')).toBeVisible()
  await page.locator('.issue-options-due-date').getByRole('option', { name: /^Tomorrow/ }).click()
  await expect(page.locator('.issue-mobile-properties .issue-due-date-trigger')).toBeVisible()
  await noOverflow(page)
})

test('comment composer and attachment remain usable on a small phone', async ({ page, request }) => {
  await page.setViewportSize({ width: 375, height: 667 })
  const upload = await request.post(`/api/issues/${child.id}/attachments`, { headers: { 'X-Workspace-Key': workspaceKey }, multipart: { embed: '0', file: { name: '[mobile] notes.txt', mimeType: 'text/plain', buffer: Buffer.from('[mobile] Issue detail attachment') } } })
  expect(upload.ok()).toBe(true)
  await openIssue(page, child.identifier)
  await expect(page.locator('.attachment-card')).toHaveCount(1)
  await expect(page.locator('.attachment-card a').last()).toContainText('[mobile] notes.txt')
  const composer = page.locator('.issue-document > .composer')
  await composer.scrollIntoViewIfNeeded()
  await composer.getByRole('textbox').fill('[mobile] A phone-sized comment with a long token: ' + 'x'.repeat(100))
  await composer.getByRole('button', { name: 'Submit comment', exact: true }).click()
  await expect(page.locator('.comment-body').filter({ hasText: '[mobile] A phone-sized comment' })).toBeVisible()
  await expect(page.locator('.comment-card__actions')).toHaveCSS('opacity', '1')
  await noOverflow(page)
  if (shots) await page.screenshot({ path: `${shots}/flow-comments-375.png`, scale: 'css' })
})

test('phone title and description edits persist', async ({ page, request }) => {
  await openIssue(page, child.identifier)
  await page.getByRole('textbox', { name: 'Issue title', exact: true }).fill('[mobile] Edited phone title')
  await page.getByRole('textbox', { name: 'Issue description', exact: true }).fill('[mobile] Description edited on a phone')
  await page.getByRole('textbox', { name: 'Issue title', exact: true }).click()
  await expect.poll(async () => {
    const response = await request.get(`/api/issue-records/${child.id}`, { headers: { 'X-Workspace-Key': workspaceKey } })
    const issue = await response.json()
    return { title: issue.title, description: issue.description }
  }).toEqual({ title: '[mobile] Edited phone title', description: '\\[mobile\\] Description edited on a phone' })
  await page.reload()
  await expect(page.getByRole('textbox', { name: 'Issue title', exact: true })).toHaveText('[mobile] Edited phone title')
  await expect(page.getByRole('textbox', { name: 'Issue description', exact: true })).toHaveText('[mobile] Description edited on a phone')
  await noOverflow(page)
})

test('desktop keeps the properties rail and floating work controls', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await openIssue(page)
  await expect(page.locator('.issue-mobile-properties')).toBeHidden()
  await expect(page.locator('.issue-properties-rail')).toBeVisible()
  await expect(page.locator('.issue-work-control')).toBeVisible()
  expect((await page.locator('.issue-header').boundingBox())?.height).toBe(44)
  await noOverflow(page)
})
