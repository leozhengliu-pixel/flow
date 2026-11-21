import { expect } from '@playwright/test'
import { join } from 'node:path'
import { readLocalRow, test } from './fixture'

test.use({ workspaceNamePrefix: 'Partials' })

test('lays out retained issue history and archive pages at desktop and phone widths', async ({ context, request, workspaceKey }, info) => {
  test.skip(process.env.FLOW_LOCAL_STORE_REAL_AUTH !== '1', 'Requires a browser session with an editable profile')
  const shots = process.env.FLOW_LOCAL_STORE_SHOTS
  await context.addCookies((await request.storageState()).cookies)
  const headers = { 'X-Workspace-Key': workspaceKey }
  const { teams: [team], viewer } = await (await request.get('/api/bootstrap', { headers })).json()
  expect((await request.patch('/api/account/profile', { headers, data: {
    displayName: '\u590f \u96e8', username: viewer.username || 'storage-tester',
  } })).ok()).toBe(true)
  const labelResponse = await request.post('/api/labels', { headers, data: { name: '\u590d\u6838', color: '#676f88' } })
  expect(labelResponse.status()).toBe(201)
  const { id: labelId } = await labelResponse.json()
  const create = async (title: string, archived = false) => {
    const response = await request.post('/api/issues', { headers, data: {
      teamId: team.id, title, labelIds: [labelId], assigneeId: viewer.id,
    } })
    expect(response.status()).toBe(201)
    const issue = await response.json()
    if (archived) expect((await request.patch(`/api/issues/${issue.id}`, { headers, data: { archived: true } })).ok()).toBe(true)
    return issue
  }
  const shortTitle = '\u6838\u5bf9'
  const longTitle = 'Review the translated notification labels and shared workspace preferences before the next release'
  const issues = [await create(shortTitle), await create(longTitle)]
  const archived = await create(`Archived: ${longTitle}`, true)
  const comment = 'Saved in the authorized history slice'
  for (const issue of issues) {
    expect((await request.post(`/api/issues/${issue.id}/comments`, { headers, data: { body: comment } })).status()).toBe(201)
  }
  const prefix = `/${workspaceKey}/team/${team.key.toLowerCase()}`
  const phone = info.project.name === 'webkit' ? 375 : 390
  for (const [width, dark] of [[1440, false], [phone, true]] as const) {
    const issue = dark ? issues[1] : issues[0]
    const language = dark ? 'zh-CN' : 'en-US'
    const theme = dark ? 'dark' : 'light'
    expect((await request.patch('/api/account/settings', { headers, data: {
      pulseSchedule: 'never', language, interfaceTheme: dark ? 'Dark' : 'Light', timezone: 'UTC',
    } })).ok()).toBe(true)
    const open = async () => {
      const page = await context.newPage()
      await page.setViewportSize({ width, height: width === 1440 ? 900 : 844 })
      await page.emulateMedia({ colorScheme: theme })
      return page
    }
    const page = await open()
    await page.goto(`${prefix}/all`, { waitUntil: 'commit' })
    const row = page.locator(`[data-issue-row-id="${issue.id}"]`)
    await expect(row).toBeVisible()
    await expect.poll(() => readLocalRow(page, issue.id)).toMatchObject({ title: issue.title })
    await row.click()
    const title = page.locator('.title-editor')
    await expect(title).toHaveText(issue.title)
    await expect(title).toBeEditable()
    await expect(page.getByText(comment, { exact: true })).toBeVisible()
    await expect(page.locator('html')).toHaveAttribute('lang', dark ? 'zh-CN' : 'en')
    await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
    await expect(page.locator('[data-user-avatar][data-script="cjk"][data-initials="2"]:visible').first()).toHaveText('\u590f\u96e8')
    const chip = page.getByText('\u590d\u6838', { exact: true }).filter({ visible: true }).first()
    if (!dark) await expect(chip).toBeVisible()
    await expect(page.locator('.issue-subscribe-toggle')).toBeDisabled()
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
    const filename = `${info.project.name}-${width}-${theme}-${language}`
    if (shots) await page.screenshot({ path: join(shots, `detail-${filename}.png`), animations: 'disabled' })
    await page.keyboard.press('Tab')
    await title.focus()
    await expect(title).toBeFocused()
    if (dark) {
      const labels = page.locator('.issue-mobile-properties .labels-picker .label-project-trigger')
      await expect(labels).toHaveText('1')
      await labels.click()
      await expect(chip).toBeVisible()
      if (shots) await page.screenshot({ path: join(shots, `labels-${filename}.png`), animations: 'disabled' })
    }
    await page.close()

    const archivePage = await open()
    await archivePage.goto(`${prefix}/archive/issues`, { waitUntil: 'commit' })
    const archive = archivePage.locator('.archive-row').filter({ hasText: archived.title })
    await expect(archive).toBeVisible()
    await expect(archivePage.locator('.archive-tabs [aria-current="page"]')).toBeVisible()
    expect(await archivePage.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width)
    if (shots) await archivePage.screenshot({ path: join(shots, `archive-${filename}.png`), animations: 'disabled' })
    const menu = archive.getByRole('button', { includeHidden: true })
    await menu.hover()
    await expect(menu).toHaveCSS('opacity', '1')
    await archivePage.keyboard.press('Tab')
    await menu.focus()
    await expect(menu).toBeFocused()
    expect(await menu.evaluate(element => element.matches(':focus-visible'))).toBe(true)
    if (shots) await archive.screenshot({ path: join(shots, `archive-focus-${filename}.png`), animations: 'disabled' })
    await menu.press('Enter')
    await expect(archivePage.getByRole('menuitem')).toBeVisible()
    await expect(menu).toHaveAttribute('aria-expanded', 'true')
    if (shots) await archivePage.getByRole('menu').screenshot({ path: join(shots, `archive-menu-${filename}.png`), animations: 'disabled' })
    await archivePage.keyboard.press('Escape')
    await archivePage.close()
  }
})
