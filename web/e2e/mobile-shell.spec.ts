import { expect, type Locator, type Page } from '@playwright/test'
import { test } from './workspace-fixture'

const userAgent = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1'
const base = process.env.FLOW_MOBILE_BASE_URL ?? 'http://127.0.0.1:4173'
const api = process.env.FLOW_MOBILE_API_URL ?? 'http://127.0.0.1:4180'
const shots = process.env.FLOW_MOBILE_SHOTS

test.use({ colorScheme: 'dark' })

async function geometry(locator: Locator, expected: Partial<{ x: number; y: number; width: number; height: number }>) {
  await expect(locator).toBeVisible()
  await expect.poll(async () => {
    const box = await locator.boundingBox()
    return box && Object.entries(expected).every(([key, value]) => Math.abs(box[key as keyof typeof box] - value) <= 1)
  }).toBeTruthy().catch(async error => {
    console.error({ expected, actual: await locator.boundingBox(), className: await locator.getAttribute('class') })
    throw error
  })
}

async function noOverflow(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
}

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test.describe(`${viewport.width}px shell`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent, colorScheme: 'dark' })

    test('workspace panel keeps compact labels, both dividers and a working filter', async ({ page, workspaceKey }) => {
      await page.goto(`${base}/${workspaceKey}/my-issues/assigned`)
      await page.locator('[data-sidebar-trigger]').tap()
      await expect(page.locator('.sidebar-primary-links a[href$="/inbox"] svg path')).toHaveAttribute('d', /^M11.0069 1.00879/)
      await page.locator('.workspace-menu-trigger').tap()
      const menu = page.getByRole('dialog', { name: 'Workspace Menu', exact: true })
      await geometry(menu, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32, height: 312 })
      await expect(menu.getByRole('option')).toHaveCount(5)
      for (const name of ['Settings', 'Invite and manage members', 'Download desktop app', 'Switch workspace', 'Log out']) {
        const row = menu.getByRole('option', { name, exact: true })
        await geometry(row, { x: 22.5, width: viewport.width - 44, height: 46 })
        await expect(row.locator('span')).toHaveCSS('font-size', '13px')
        await expect(row.locator('span')).toHaveCSS('font-weight', '450')
        await expect(row.locator('span')).toHaveCSS('line-height', '15.6px')
      }
      await expect(menu.locator('[cmdk-separator] > span')).toHaveCount(2)
      await noOverflow(page)
      if (shots) await page.screenshot({ path: `${shots}/workspace-menu-${viewport.width}.png` })
      await menu.getByRole('combobox').fill('Switch')
      await expect(menu.getByRole('option')).toHaveCount(1)
      await menu.getByRole('option', { name: 'Switch workspace', exact: true }).tap()
      const switcher = page.getByRole('dialog', { name: 'Switch workspace', exact: true })
      await expect(switcher.getByRole('combobox')).toHaveValue('')
      await expect(switcher.getByRole('option', { name: new RegExp(`Verify ${workspaceKey.slice(-8)}$`) })).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(switcher).toHaveCount(0)
      await page.locator('.workspace-menu-trigger').tap()
      await menu.getByRole('option', { name: 'Invite and manage members', exact: true }).tap()
      await expect(page).toHaveURL(/\/settings\/members$/)
    })

    test('bottom drawer keeps expanded cycles and Import issues clear of its pinned footer', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const bootstrap = await (await request.get(`${api}/api/bootstrap`, { headers })).json() as { teams: Array<{ id: string }> }
      const secondResponse = await request.post(`${api}/api/workspaces/${workspaceKey}/teams`, { headers, data: { name: '[mobile] Second team', key: 'SECOND' } })
      expect(secondResponse.status()).toBe(201)
      const second = await secondResponse.json() as { id: string }
      const thirdResponse = await request.post(`${api}/api/workspaces/${workspaceKey}/teams`, { headers, data: { name: '[mobile] Third team', key: 'THIRD', parentTeamId: second.id } })
      expect(thirdResponse.status()).toBe(201)
      const third = await thirdResponse.json() as { id: string }
      for (const team of [bootstrap.teams[0], second, third]) {
        expect((await request.patch(`${api}/api/teams/${team.id}/settings`, { headers, data: { showInitiatives: false } })).ok()).toBe(true)
      }
      expect((await request.patch(`${api}/api/teams/${second.id}/cycle-settings`, { headers, data: { enabled: true } })).ok()).toBe(true)
      await page.goto(`${base}/${workspaceKey}/my-issues/assigned`)
      await page.locator('[data-sidebar-trigger]').tap()
      const drawer = page.locator('[data-sidebar-surface]')
      const secondTeam = drawer.locator('.sidebar-team').filter({ has: page.locator('.team-title', { hasText: '[mobile] Second team' }) })
      await drawer.locator('.team-title').filter({ hasText: '[mobile] Third team' }).tap()
      for (const label of ['Invite people', 'Cycles', 'Connect GitHub']) {
        const dismiss = drawer.getByRole('button', { name: `Dismiss ${label}`, exact: true })
        if (await dismiss.count()) await dismiss.tap()
      }
      const nav = drawer.locator('.sidebar-nav')
      await nav.evaluate(e => { e.scrollTop = e.scrollHeight })
      await expect.poll(() => nav.evaluate(e => e.scrollHeight - e.clientHeight - e.scrollTop)).toBeLessThanOrEqual(1)
      for (const label of ['Home', 'Issues', 'Cycles', 'Projects', 'Views']) {
        await geometry(secondTeam.getByRole('link', { name: label, exact: true }), { x: 31, width: 287, height: 28 })
      }
      for (const label of ['Current', 'Upcoming']) {
        const row = secondTeam.getByRole('link', { name: label, exact: true })
        await geometry(row, { x: 51, width: 267, height: 28 })
        await expect(row.locator('.nav-label')).toHaveCSS('font-size', '13px')
        expect(await row.locator('.nav-label').evaluate(e => {
          const range = document.createRange(); range.selectNodeContents(e)
          return range.getBoundingClientRect().x
        })).toBe(57)
      }
      const heading = await secondTeam.locator('.team-heading').boundingBox()
      const home = await secondTeam.getByRole('link', { name: 'Home', exact: true }).boundingBox()
      expect(home!.y + home!.height / 2 - heading!.y - heading!.height / 2).toBe(33)
      await geometry(drawer.locator('.sidebar-footer'), { y: viewport.height - 52, height: 52 })
      const importIssues = drawer.getByRole('link', { name: 'Import issues', exact: true })
      await geometry(importIssues, { x: 12, y: viewport.height - 81, width: 306, height: 28 })
      await expect(importIssues.locator('.nav-icon path').first()).toHaveAttribute('d', /^M13.25 5.25/)
      await expect(drawer.locator('.sidebar-compose')).toHaveCSS('background-color', 'lch(10.149 0.689 272)')
      await expect(page.locator('.bottom-agent')).toBeHidden()
      await noOverflow(page)
      if (shots) await page.screenshot({ path: `${shots}/drawer-bottom-${viewport.width}.png` })
      await importIssues.tap()
      await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/import-export$`))
    })

    test('phone drawer labels, floating footer, glyphs and focus remain usable', async ({ page, request }) => {
      const key = `mobile-drawer-parity-${viewport.width}-${Date.now()}`
      const created = await request.post(`${api}/api/workspaces`, {
        data: { name: '[mobile] Drawer parity', urlKey: key, region: 'us', tutorial: false },
      })
      expect(created.status()).toBe(201)
      const headers = { 'X-Workspace-Key': key }
      for (let i = 0; i < 5; i++) {
        const team = await request.post(`${api}/api/workspaces/${key}/teams`, { headers, data: { name: `[mobile] Drawer team ${i}`, key: `DR${i}` } })
        expect(team.status()).toBe(201)
      }
      await page.goto(`${base}/${key}/my-issues/assigned`)
      await page.locator('[data-sidebar-trigger]').tap()
      const drawer = page.locator('[data-sidebar-surface]')
      const workspace = drawer.locator('.workspace-menu-trigger')
      await expect(workspace).toBeFocused()
      expect(await workspace.evaluate(e => e.matches(':focus-visible'))).toBe(false)
      for (const label of await drawer.locator('.nav-label, .sidebar-more-trigger > span').all()) {
        await expect(label).toHaveCSS('font-size', '13px')
        await expect(label).toHaveCSS('font-weight', '500')
      }
      await geometry(drawer.locator('.sidebar-primary-links .nav-label').first(), { x: 40 })
      await geometry(drawer.locator('.team-links .nav-label').first(), { x: 57 })
      await geometry(drawer.locator('.sidebar-footer'), { x: 0, y: viewport.height - 52, width: 330, height: 52 })
      await expect(drawer.locator('.sidebar-footer')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
      const help = drawer.locator('.sidebar-help')
      await geometry(help, { x: 10, y: viewport.height - 42, width: 32, height: 32 })
      await expect(help.locator('use')).toHaveAttribute('href', '#QuestionMark')
      await geometry(help.locator('use'), { x: 23.52, y: viewport.height - 30.38, width: 5.2, height: 8.75 })
      const fill = await help.evaluate(e => {
        const s = getComputedStyle(e, '::before')
        return { background: s.backgroundColor, width: s.width, height: s.height }
      })
      expect(fill).toEqual({ background: 'lch(5.52 0.4 272)', width: '32px', height: '32px' })
      const home = drawer.locator('.team-links a').filter({ hasText: 'Home' }).first()
      expect(await home.locator('.nav-icon path').getAttribute('d')).toContain('M6.56055 1.52061')
      const nav = drawer.locator('.sidebar-nav')
      await page.mouse.move(240, viewport.height - 25)
      await page.mouse.wheel(0, 350)
      await expect.poll(() => nav.evaluate(e => e.scrollTop)).toBeGreaterThan(0)
      await nav.evaluate(e => { e.scrollTop = 0 })
      await geometry(help, { x: 10, y: viewport.height - 42 })
      await page.keyboard.press('Tab')
      await page.keyboard.press('Shift+Tab')
      await expect(workspace).toBeFocused()
      expect(await workspace.evaluate(e => e.matches(':focus-visible'))).toBe(true)
      expect(await workspace.evaluate(e => getComputedStyle(e).outlineStyle)).not.toBe('none')
      if (shots) await page.screenshot({ path: `${shots}/drawer-parity-${viewport.width}.png` })
    })

    test('navigation, workspace menus, search, commands and composer geometry', async ({ page, workspaceKey }) => {
      await page.goto(`${base}/${workspaceKey}/inbox`)
      const main = page.locator('.app > main, .app > .flow-framed-workspace')
      await geometry(main, { x: 0, y: 0, width: viewport.width, height: viewport.height })
      const trigger = page.locator('[data-sidebar-trigger]').first()
      await geometry(trigger, { x: 8, width: 32, height: 32 })
      await noOverflow(page)
      await trigger.click()
      const drawer = page.locator('[data-sidebar-surface]')
      await geometry(drawer, { x: 0, y: 0, width: 330, height: viewport.height })
      await geometry(drawer.locator('.nav-item').first(), { x: 12, y: 56, height: 28, width: 306 })
      await geometry(drawer.locator('.sidebar-compose'), { x: 286, width: 32, height: 32 })
      await expect(drawer.locator('.sidebar-compose')).toHaveCSS('background-color', 'lch(10.149 0.689 272)')
      await geometry(drawer.locator('.sidebar-header'), { x: 0, y: 0, width: 330, height: 51.5 })
      await geometry(drawer.locator('.sidebar-footer'), { x: 0, y: viewport.height - 52, width: 330, height: 52 })
      await geometry(drawer.locator('.sidebar-help'), { x: 10, y: viewport.height - 42, width: 32, height: 32 })
      await geometry(drawer.locator('.sidebar-plan-button'), { x: 50, y: viewport.height - 42, height: 32 })
      if (await drawer.locator('.sidebar-team .team-heading').count()) {
        await geometry(drawer.locator('.sidebar-team .team-heading').first(), { x: 12, width: 306, height: 36 })
      }
      if (shots) await page.screenshot({ path: `${shots}/flow-drawer-${viewport.width}.png` })

      await drawer.locator('.workspace-menu-trigger').click()
      const menu = page.locator('.phone-menu-surface')
      await geometry(menu, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32 })
      await geometry(menu.locator('[cmdk-item]').first(), { height: 46 })
      await menu.getByRole('option', { name: 'Switch workspace', exact: true }).click()
      await expect(menu.getByRole('option', { name: new RegExp(`Verify ${workspaceKey.slice(-8)}$`) })).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(menu).not.toBeVisible()
      await page.locator('.sidebar-scrim.open').click({ position: { x: viewport.width - 12, y: 100 } })
      await expect(drawer).toHaveAttribute('aria-hidden', 'true')

      await trigger.click()
      await drawer.getByRole('button', { name: 'Search', exact: true }).click()
      await expect(page).toHaveURL(/\/search$/)
      await expect(drawer).toHaveAttribute('aria-hidden', 'true')
      await geometry(page.locator('.workspace-search-header'), { height: 44 })
      await geometry(page.locator('.workspace-search-toolbar'), { height: 43.5 })
      await expect(page.getByRole('button', { name: '2 more', exact: true })).toBeVisible()
      await noOverflow(page)
      await page.getByRole('button', { name: '2 more', exact: true }).click()
      await geometry(page.locator('.phone-menu-surface'), { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32 })
      await page.locator('.phone-menu-surface input').fill('Documents')
      await expect(page.locator('.phone-menu-surface').getByRole('option')).toHaveCount(1)
      await page.locator('.phone-menu-surface').getByRole('option', { name: 'Documents' }).click()
      await expect(page).toHaveURL(/type=document/)
      await expect(page.locator('.phone-menu-surface')).not.toBeVisible()

      await page.keyboard.press('Escape')
      await page.locator('.workspace-search-header input').blur()
      await page.keyboard.press('Meta+k')
      const commands = page.locator('.command-dialog')
      await geometry(commands, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32 })
      await geometry(commands.locator('[cmdk-item]').first(), { height: 46 })
      await expect(commands.locator('[cmdk-item]').first()).toHaveCSS('font-size', '15px')
      if (shots) await page.screenshot({ path: `${shots}/flow-command-${viewport.width}.png` })
      await page.keyboard.press('Escape')

      await page.locator('[data-sidebar-trigger]').first().click()
      await drawer.getByRole('button', { name: 'Create new issue', exact: true }).click()
      const composer = page.getByRole('dialog', { name: 'New issue', exact: true })
      await geometry(composer, { x: 12, y: Math.min(110, viewport.height * .1304), width: viewport.width - 24 })
      await geometry(composer.locator('header'), { height: 60 })
      await expect(drawer).toHaveAttribute('aria-hidden', 'true')
      await geometry(composer.getByRole('button', { name: 'Close', exact: true }), { width: 36, height: 36 })
      await expect(composer.getByRole('button', { name: 'Expand', exact: true })).not.toBeVisible()
      await geometry(composer.getByRole('button', { name: 'Create issue', exact: true }), { height: 32 })
      await noOverflow(page)
      if (shots) await page.screenshot({ path: `${shots}/flow-composer-${viewport.width}.png` })
      await composer.getByRole('combobox', { name: /Change priority/i }).click()
      const picker = page.locator('.phone-menu-surface')
      await geometry(picker, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32 })
      await geometry(picker.getByRole('option').first(), { height: 46 })
      await expect(picker.getByRole('option').first()).toHaveCSS('font-size', '15px')
      if (shots) await page.screenshot({ path: `${shots}/flow-picker-${viewport.width}.png` })
      await picker.locator('input').fill('Urgent')
      await expect(picker.getByRole('option')).toHaveCount(1)
      await page.keyboard.press('Escape')
      await expect(picker).not.toBeVisible()
      await expect(composer).toBeVisible()
      await composer.getByRole('textbox', { name: 'Issue title', exact: true }).fill('[mobile] Local draft')
      await composer.getByRole('button', { name: 'Close', exact: true }).click()
      const confirm = page.getByRole('dialog', { name: 'Save to drafts?', exact: true })
      await geometry(confirm, { x: 16, width: viewport.width - 32 })
      await noOverflow(page)
      if (shots) await page.screenshot({ path: `${shots}/flow-confirm-${viewport.width}.png` })
      await confirm.getByRole('button', { name: 'Discard', exact: true }).click()
      await expect(composer).not.toBeVisible()
      await page.locator('[data-sidebar-trigger]').first().click()
      await drawer.getByRole('button', { name: 'Open Help menu' }).click()
      await geometry(page.locator('.phone-menu-surface'), { width: viewport.width - 32 })
      await geometry(page.locator('.phone-menu-surface [cmdk-item]').first(), { height: 46 })
      await page.keyboard.press('Escape')
      const github = drawer.getByRole('button', { name: 'Connect GitHub', exact: true })
      await github.focus()
      await page.keyboard.press('Enter')
      await expect(page.locator('.flow-toast')).toBeVisible()
      await page.waitForTimeout(500)
      await noOverflow(page)
      if (shots) await page.screenshot({ path: `${shots}/flow-toast-${viewport.width}.png` })
    })
  })
}

test.describe('shared search empty state', () => {
  test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent })
  test('fits the phone frame without recent history', async ({ page, workspaceKey }) => {
    await page.route('**/api/search?*', route => route.fulfill({ json: { results: [], history: [], recent: [] } }))
    await page.goto(`${base}/${workspaceKey}/search`)
    await expect(page.locator('.workspace-search-empty')).toBeVisible()
    await geometry(page.locator('.workspace-search-header input'), { x: 66, height: 22 })
    await geometry(page.locator('.workspace-search-empty-illustration'), { x: 105, y: 378.25, width: 180, height: 120 })
    await geometry(page.locator('.workspace-search-empty strong'), { x: 173.17, y: 514.25 })
    await noOverflow(page)
    if (shots) await page.screenshot({ path: `${shots}/flow-search-390.png` })
  })
})

test.describe('main page headers', () => {
  test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent })
  test('keeps phone navigation and page actions aligned', async ({ page, workspaceKey }) => {
    for (const route of ['my-issues/assigned', 'projects/all', 'initiatives/active', 'reviews', 'agent']) {
      await page.goto(`${base}/${workspaceKey}/${route}`)
      await geometry(page.locator('.app > main, .app > .flow-framed-workspace'), { x: 0, y: 0, width: 390, height: 844 })
      await geometry(page.locator('[data-sidebar-trigger]').first(), { x: 8, width: 32, height: 32 })
      await geometry(page.locator('main h1, main h2').first(), { x: route === 'agent' ? 52.5 : 52 })
      await noOverflow(page)
    }
    await page.goto(`${base}/${workspaceKey}/my-issues/assigned`)
    await geometry(page.getByRole('button', { name: 'New issue', exact: true }), { x: 350, width: 32, height: 32 })
    if (shots) await page.screenshot({ path: `${shots}/flow-header-390.png` })
    await page.getByRole('button', { name: 'New issue', exact: true }).click()
    await expect(page.getByRole('dialog', { name: 'New issue', exact: true })).toBeVisible()
  })
})

test.describe('my issues phone toolbar', () => {
  test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent })
  test('matches the reference top bar, toolbar and grouped list', async ({ page, request, workspaceKey }) => {
    const headers = { 'X-Workspace-Key': workspaceKey }
    const bootstrap = await (await request.get(`${api}/api/bootstrap`, { headers })).json() as {
      viewer: { id: string }; teams: Array<{ id: string }>; states: Array<{ id: string; type: string }>
    }
    const stateId = (bootstrap.states.find(state => state.type === 'started') ?? bootstrap.states[0]).id
    for (const title of ['[mobile] QA no-PR question: explain greet.js', '[mobile] QA remove agent: slow task']) {
      const issue = await request.post(`${api}/api/issues`, {
        headers,
        data: { title, description: '', teamId: bootstrap.teams[0].id, stateId, assigneeId: bootstrap.viewer.id },
      })
      expect(issue.status()).toBe(201)
    }

    await page.goto(`${base}/${workspaceKey}/my-issues/assigned`)
    await geometry(page.locator('[data-content-view-header]'), { x: 0, y: 0, width: 390, height: 44 })
    await geometry(page.getByRole('button', { name: 'New issue', exact: true }), { x: 350, width: 32, height: 32 })
    await geometry(page.locator('[data-content-view-subheader]'), { x: 0, y: 44, width: 390, height: 43.5 })
    await geometry(page.getByRole('link', { name: /^Assigned/ }), { x: 8, height: 32 })
    await geometry(page.locator('[data-my-issues-group-header]').first(), { x: 8, y: 87.5, width: 374, height: 36 })
    await geometry(page.locator('[data-issue-row-id]').first(), { x: 0, y: 125.5, width: 390, height: 44 })
    await expect(page.locator('.bottom-agent')).toBeHidden()
    await noOverflow(page)

    // The filter menu and its value pickers use the reference's full-width phone panel, not an anchored popover.
    await page.getByRole('button', { name: 'Add filter', exact: true }).click()
    await geometry(page.getByRole('dialog', { name: 'Add Filter…' }), { x: 16, width: 358 })
    await page.getByRole('option', { name: /Status/ }).first().click()
    await geometry(page.locator('[role="dialog"][data-field="status"]'), { x: 16, width: 358 })
    await page.keyboard.press('Escape')
    await page.mouse.click(200, 22)
    await expect(page.getByRole('dialog', { name: 'Add Filter…' })).not.toBeVisible()
    if (shots) await page.screenshot({ path: `${shots}/flow-myissues-390.png` })
  })
})

test('desktop frame stays docked at 1280px', async ({ browser, workspaceKey }) => {
  const page = await browser.newPage({ viewport: { width: 1280, height: 768 } })
  await page.goto(`${base}/${workspaceKey}/inbox`)
  await expect(page.locator('[data-sidebar-surface]')).not.toHaveAttribute('aria-hidden')
  await expect(page.locator('[data-sidebar-trigger]').first()).not.toBeVisible()
  await expect(page.locator('.app > main, .app > .flow-framed-workspace')).toHaveCSS('border-top-left-radius', '12px')
  await page.close()
})
