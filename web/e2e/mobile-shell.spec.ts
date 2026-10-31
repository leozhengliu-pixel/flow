import { expect, type Locator, type Page } from '@playwright/test'
import { test } from './workspace-fixture'
import { createHmac } from 'node:crypto'

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

    test('composer assignee picker groups real candidates in the phone panel', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Flow-Workspace': workspaceKey, 'X-Workspace-Key': workspaceKey }
      const data = await (await request.get(`${api}/api/bootstrap`, { headers })).json() as { viewer: { id: string; displayName: string }; teams: Array<{ id: string }> }
      const tokenResponse = await request.post(`${api}/api/scim/tokens`, { headers, data: { name: '[mobile] Assignee fixture' } })
      expect(tokenResponse.status()).toBe(201)
      const token = await tokenResponse.json() as { id: string; secret: string }
      const member = await request.post(`${api}/scim/v2/${workspaceKey}/Users`, {
        headers: { Authorization: `Bearer ${token.secret}` },
        data: { userName: `assignee-${workspaceKey}@example.test`, displayName: '[mobile] Team member', active: true },
      })
      expect(member.status()).toBe(201)
      await request.delete(`${api}/api/scim/tokens/${token.id}`, { headers })
      const appResponse = await request.post(`${api}/api/oauth-applications`, { headers, data: { name: '[mobile] App agent', scopes: ['read', 'app:assignable'] } })
      expect(appResponse.status()).toBe(201)
      const app = await appResponse.json() as { clientId: string }
      expect((await request.post(`${api}/api/application-installations`, { headers, data: { clientId: app.clientId, teamIds: [data.teams[0].id], scopes: ['read', 'app:assignable'], active: true } })).status()).toBe(200)

      await page.goto(`${base}/${workspaceKey}/my-issues/assigned`)
      await page.getByRole('button', { name: 'New issue', exact: true }).click()
      const composer = page.getByRole('dialog', { name: 'New issue', exact: true })
      await composer.locator('[data-property=assignee]').click()
      let picker = page.locator('.phone-menu-surface:has([role=listbox][aria-label=Assignee])')
      await geometry(picker, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32, height: 382 })
      await expect(picker.getByText('Assigned users', { exact: true })).toBeVisible()
      await expect(picker.getByText('Other agents', { exact: true })).toBeVisible()
      await expect(picker.getByText('Team members', { exact: true })).toBeVisible()
      await geometry(picker.getByRole('option').first(), { height: 46 })
      await expect(picker.locator('.property-command-label').first()).toHaveCSS('font-size', '13px')
      await expect(picker.locator('.property-command-label').first()).toHaveCSS('font-weight', '450')
      await expect(picker.locator('kbd').first()).toBeHidden()
      await picker.getByRole('option').filter({ has: page.locator('.property-command-label', { hasText: data.viewer.displayName }) }).click()
      await expect(picker).not.toBeVisible()
      await composer.locator('[data-property=assignee]').click()
      picker = page.locator('.phone-menu-surface:has([role=listbox][aria-label=Assignee])')
      const viewer = picker.getByRole('option').filter({ has: page.locator('.property-command-label', { hasText: data.viewer.displayName }) })
      await expect(viewer).toHaveAttribute('aria-checked', 'true')
      await geometry(viewer.locator('.core-person-picker-avatar'), { width: 18, height: 18 })
      await expect(viewer.locator('.property-command-check svg')).toBeVisible()
      await noOverflow(page)
      await page.waitForTimeout(600)
      if (shots) await page.screenshot({ path: `${shots}/flow-assignee-${viewport.width}.png` })
      await picker.getByRole('textbox').fill('[mobile] App agent')
      await expect(picker.getByRole('option')).toHaveCount(1)
      await page.keyboard.press('Escape')
      await expect(picker).not.toBeVisible()
      await expect(composer).toBeVisible()
    })

    test('issue actions menu matches phone geometry and filters its real actions', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const data = await (await request.get(`${api}/api/bootstrap`, { headers })).json() as { teams: Array<{ id: string }> }
      const created = await request.post(`${api}/api/issues`, {
        headers, data: { title: '[mobile] Issue actions', teamId: data.teams[0].id },
      })
      expect(created.status()).toBe(201)
      const issue = await created.json() as { identifier: string }
      const secret = `actions-${workspaceKey}`
      expect((await request.put(`${api}/api/integrations/github`, {
        headers, data: { name: '[mobile] GitHub', config: { organization: 'mobile-e2e', webhookSecret: secret, codeAccess: 'true' }, scopes: ['repo'] },
      })).status()).toBe(200)
      const body = JSON.stringify({ action: 'opened', number: 1, pull_request: { number: 1, id: 9101, title: '[mobile] Review', body: '', html_url: 'https://github.com/mobile-e2e/repo/pull/1', state: 'open', user: { login: 'flowuser' }, base: { ref: 'main' }, head: { ref: 'mobile/review', sha: 'abc' }, additions: 3, deletions: 1, commits: 1 }, repository: { full_name: 'mobile-e2e/repo' }, sender: { login: 'flowuser' } })
      expect((await request.post(`${api}/api/integrations/github/webhook`, {
        headers: { ...headers, 'Content-Type': 'application/json', 'X-GitHub-Event': 'pull_request', 'X-GitHub-Delivery': `actions-${workspaceKey}`, 'X-Hub-Signature-256': 'sha256=' + createHmac('sha256', secret).update(body).digest('hex') }, data: body,
      })).status()).toBe(202)

      await page.goto(`${base}/${workspaceKey}/issue/${issue.identifier}`)
      await page.getByRole('button', { name: 'Issue options', exact: true }).tap()
      const menu = page.getByRole('dialog', { name: 'Issue options', exact: true })
      const top = Math.min(110, viewport.height * .1304)
      await geometry(menu, { x: 16, y: top, width: viewport.width - 32, height: 450 })
      await geometry(menu.locator('.issue-options-search'), { x: 22.5, y: top + 6.5, width: viewport.width - 44, height: 40 })
      const names = ['Team', 'Due date', 'Add link…', 'Add customer request…', 'Add pull request…', 'Add document…', 'Create related', 'Mark as']
      for (const [index, name] of names.entries()) {
        const label = menu.locator('.issue-options-label').filter({ hasText: new RegExp(`^${name}$`) })
        const row = menu.getByRole('option').filter({ has: page.locator(`.issue-options-label:text-is("${name}")`) })
        await geometry(row, { x: 22.5, y: top + 52.5 + index * 46 + (index >= 6 ? 12 : 0), width: viewport.width - 44, height: 46 })
        await expect(label).toHaveCSS('font-size', '13px')
        await expect(label).toHaveCSS('font-weight', '450')
        await expect(label).toHaveCSS('line-height', '15.6px')
        await geometry(row.locator('.issue-options-icon svg'), { width: 16, height: 16 })
      }
      await geometry(menu.locator('.issue-options-separator-line').first(), { x: 16.5, y: top + 334.5, width: viewport.width - 32, height: .5 })
      await expect(menu).toHaveCSS('background-color', 'lch(12.72 0.85 272)')
      await expect(menu).toHaveCSS('border-radius', '12px')
      await expect(page.locator('.bottom-agent')).toBeHidden()
      await noOverflow(page)
      if (shots) await page.screenshot({ path: `${shots}/actions-menu-${viewport.width}.png` })
      const filter = menu.getByRole('combobox')
      await filter.fill('Add link')
      await expect(menu.getByRole('option')).toHaveCount(1)
      await geometry(menu.getByRole('option'), { y: top + 52.5, height: 46 })
      await filter.fill('')
      await menu.getByRole('option', { name: 'Team', exact: true }).tap()
      const team = page.getByRole('dialog', { name: 'Team', exact: true })
      await geometry(team, { x: 16, y: top, width: viewport.width - 32 })
      await expect(menu).toBeHidden()
      await team.getByRole('button', { name: 'Back', exact: true }).tap()
      await expect(menu).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(menu).toBeHidden()
    })

    test('Projects toolbar uses phone glyphs and viewport-width filter panels', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Flow-Workspace': workspaceKey }
      const data = await (await request.get(`${api}/api/bootstrap`, { headers })).json() as { teams: Array<{ id: string }> }
      const created = await request.post(`${api}/api/projects`, { headers, data: { name: '[mobile] Toolbar project', teamId: data.teams[0].id } })
      expect(created.status()).toBe(201)
      await page.goto(`${base}/${workspaceKey}/projects/all`)
      const toolbar = page.locator('.lp-projects__toolbar')
      await geometry(page.locator('.lp-projects'), { x: 0, y: 0, width: viewport.width, height: viewport.height })
      await geometry(page.locator('.lp-projects__header'), { x: 0, y: 0, width: viewport.width, height: 44 })
      await geometry(toolbar, { x: 0, y: 44, width: viewport.width, height: 43.5 })
      await geometry(page.locator('.lp-projects__header [data-sidebar-trigger]'), { x: 8, y: 5.75, width: 32, height: 32 })
      await expect(page.locator('.lp-projects__header [data-sidebar-trigger] path')).toHaveAttribute('d', /^M4.25 2C2.45508/)
      await expect(toolbar.locator('.lp-projects__phone-sidebar-icon path')).toHaveAttribute('d', /^M4.25 2C2.45508/)
      await expect(toolbar.locator('.lp-projects__desktop-sidebar-icon')).toBeHidden()
      await geometry(page.getByRole('button', { name: 'New project', exact: true }), { x: viewport.width - 40, y: 5.75, width: 32, height: 32 })
      await geometry(toolbar.getByRole('link', { name: 'All projects', exact: true }), { x: 8, y: 49.75, height: 32 })
      await geometry(toolbar.getByRole('button', { name: 'Add new view', exact: true }), { x: 97.27, y: 49.75, width: 32, height: 32 })
      await expect(page.locator('.lp-project-row__name')).toContainText('[mobile] Toolbar project')
      await expect(page.locator('.bottom-agent')).toBeHidden()
      await noOverflow(page)
      if (shots) await page.screenshot({ path: `${shots}/projects-${viewport.width}.png` })

      await toolbar.getByRole('button', { name: 'Add filter', exact: true }).tap()
      const filters = page.getByRole('dialog', { name: 'Project filters', exact: true })
      await geometry(filters, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32 })
      await geometry(filters.getByRole('option').first(), { height: 46 })
      await filters.getByRole('option', { name: 'Status', exact: true }).tap()
      const values = page.getByRole('dialog', { name: 'Status filters', exact: true })
      await geometry(values, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32 })
      await geometry(values.getByRole('option').first(), { height: 46 })
      await expect(filters.getByRole('searchbox')).toBeHidden()
      if (shots && viewport.width === 390) await page.screenshot({ path: `${shots}/projects-status-filter.png` })
      await values.getByRole('textbox').fill('Backlog')
      await values.getByRole('option').first().tap()
      await expect(filters).toBeHidden()
      await noOverflow(page)
      await toolbar.getByRole('button', { name: 'Display options', exact: true }).tap()
      const display = page.getByRole('dialog', { name: 'Display options', exact: true })
      await geometry(display, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32 })
      await display.getByRole('combobox').first().tap()
      const grouping = display.getByRole('listbox', { name: 'Choose Grouping', exact: true })
      await geometry(grouping, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32 })
      if (shots && viewport.width === 390) await page.screenshot({ path: `${shots}/projects-grouping.png` })
      await grouping.getByRole('option', { name: 'No grouping', exact: true }).tap()
      await expect(display).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(display).toBeHidden()
      await noOverflow(page)
    })

    test('composer status picker matches phone rows and preserves the workspace workflow', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const data = await (await request.get(`${api}/api/bootstrap`, { headers })).json() as { teams: Array<{ id: string }> }
      const review = await request.post(`${api}/api/teams/${data.teams[0].id}/states`, {
        headers, data: { name: 'In Review', type: 'started', color: 'lch(60% 64.37 141.95)', position: 3 },
      })
      expect(review.status()).toBe(201)
      await page.goto(`${base}/${workspaceKey}/my-issues/assigned`)
      await page.getByRole('button', { name: 'New issue', exact: true }).click()
      const composer = page.getByRole('dialog', { name: 'New issue', exact: true })
      await composer.locator('[data-property="status"]').click()
      const picker = page.getByRole('dialog', { name: 'Change status', exact: true })
      await geometry(picker, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32 })
      const options = picker.getByRole('option')
      // Reserved and custom statuses remain driven by the workspace, never by phone layout.
      await expect(options).toHaveCount(7)
      for (const name of ['Backlog', 'Todo', 'In Progress', 'In Review', 'Done', 'Canceled', 'Duplicate']) {
        const option = options.filter({ has: page.locator('.property-command-label', { hasText: new RegExp(`^${name}$`) }) })
        await geometry(option, { height: 46 })
        await expect(option.locator('.property-command-label')).toHaveCSS('font-size', '13px')
        await expect(option.locator('.property-command-label')).toHaveCSS('font-weight', '450')
      }
      await expect(options.filter({ hasText: 'Backlog' })).toHaveAttribute('aria-checked', 'true')
      await expect(options.filter({ hasText: 'Backlog' }).locator('.property-command-check svg')).toBeVisible()
      await expect(page.locator('.bottom-agent')).toBeHidden()
      await noOverflow(page)
      if (shots) await page.screenshot({ path: `${shots}/composer-status-${viewport.width}.png` })
      await picker.getByRole('textbox').fill('In Review')
      await expect(options).toHaveCount(1)
      await options.first().click()
      await expect(picker).toBeHidden()
      await expect(composer.locator('[data-property="status"]')).toHaveText('In Review')
      await composer.locator('[data-property="priority"]').click()
      const priority = page.getByRole('dialog', { name: 'Change priority', exact: true })
      await geometry(priority, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32, height: 322 })
      const priorityOptions = priority.getByRole('option')
      await expect(priorityOptions).toHaveCount(5)
      for (const name of ['No priority', 'Urgent', 'High', 'Medium', 'Low']) {
        const option = priorityOptions.filter({ has: page.locator('.property-command-label', { hasText: new RegExp(`^${name}$`, 'i') }) })
        await geometry(option, { x: 22.5, width: viewport.width - 44, height: 46 })
        await expect(option.locator('.property-command-label')).toHaveCSS('font-size', '13px')
        await expect(option.locator('.property-command-label')).toHaveCSS('font-weight', '450')
      }
      await expect(priorityOptions.filter({ hasText: 'No priority' })).toHaveAttribute('aria-checked', 'true')
      await expect(priorityOptions.filter({ hasText: 'No priority' }).locator('.property-command-check svg')).toBeVisible()
      await noOverflow(page)
    })

    test('composer labels picker matches workspace label rows', async ({ page, workspaceKey }) => {
      await page.goto(`${base}/${workspaceKey}/my-issues/assigned`)
      await page.getByRole('button', { name: 'New issue', exact: true }).click()
      const composer = page.getByRole('dialog', { name: 'New issue', exact: true })
      await composer.locator('[data-property="labels"]').click()
      const picker = page.locator('.phone-menu-surface:has([role=listbox][aria-label=Labels])')
      await geometry(picker, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32, height: 260 })
      await expect(picker.locator('.phone-menu-context')).toHaveText('New issue')
      await expect(picker.locator('.property-command-search input')).toHaveValue('')
      await expect(picker.locator('.property-command-search kbd')).toBeHidden()
      await expect(picker.locator('.property-command-search input')).toHaveAttribute('placeholder', 'Add labels…')
      await expect(picker.locator('.property-command-group')).toHaveText('Workspace labels')
      const options = picker.getByRole('option')
      await expect(options).toHaveCount(3)
      for (const name of ['Bug', 'Feature', 'Improvement']) {
        const option = options.filter({ has: page.locator('.property-command-label', { hasText: new RegExp(`^${name}$`) }) })
        await geometry(option, { x: 22.5, width: viewport.width - 44, height: 46 })
        await expect(option).toHaveAttribute('aria-checked', 'false')
        await expect(option.locator('.property-command-label')).toHaveCSS('font-size', '13px')
        await expect(option.locator('.property-command-label')).toHaveCSS('font-weight', '450')
      }
      await expect(picker.locator('.option-dot').nth(0)).toHaveCSS('background-color', 'rgb(235, 87, 87)')
      await expect(picker.locator('.option-dot').nth(1)).toHaveCSS('background-color', 'rgb(187, 135, 252)')
      await expect(picker.locator('.option-dot').nth(2)).toHaveCSS('background-color', 'rgb(78, 167, 252)')
      await expect(page.locator('.bottom-agent')).toBeHidden()
      await noOverflow(page)
      if (shots) await page.screenshot({ path: `${shots}/composer-labels-${viewport.width}.png` })
    })

    test('search phone header, empty state and filter panels match', async ({ page, workspaceKey }) => {
      await page.goto(`${base}/${workspaceKey}/search`)
      const input = page.getByRole('textbox', { name: 'Search issues, projects, and documents…' })
      await expect(input).toBeFocused()
      await expect(input).toHaveValue('')
      await geometry(page.getByTestId('search-topbar'), { x: 0, y: 0, width: viewport.width, height: 87.5 })
      await geometry(page.getByTestId('search-field'), { x: 42, y: 10.75, width: viewport.width - 50, height: 22 })
      await geometry(input, { x: 66, y: 10.75, height: 22 })
      await geometry(page.locator('.workspace-search-header [data-sidebar-trigger]'), { x: 8, y: 5.75, width: 32, height: 32 })
      const tabs = page.getByRole('navigation', { name: 'Search resource type' })
      await expect(tabs.getByRole('button', { name: 'All', exact: true })).toHaveAttribute('aria-pressed', 'true')
      for (const [name, x] of [['All', 8], ['Issues', 51.55], ['Projects', 117.49], ['2 more', 193.29]] as const) {
        await geometry(tabs.getByRole('button', { name, exact: true }), { x, y: 49.75, height: 32 })
      }
      await expect(page.locator('.workspace-search-empty')).toBeVisible()
      await geometry(page.locator('.workspace-search-empty-illustration'), {
        x: viewport.width === 390 ? 105 : 97.5,
        y: viewport.width === 390 ? 378.25 : 289.75,
        width: 180,
        height: 120,
      })
      await geometry(page.locator('.workspace-search-empty > div > strong'), {
        x: viewport.width === 390 ? 173.17 : 165.67,
        y: viewport.width === 390 ? 514.25 : 425.75,
        height: 15.5,
      })
      await geometry(page.locator('.workspace-search-empty > div > span'), {
        x: viewport.width === 390 ? 46.28 : 38.78,
        y: viewport.width === 390 ? 537.75 : 449.25,
        height: 15.5,
      })
      await expect(page.locator('.workspace-search-empty-illustration')).toHaveCSS('color', 'lch(100 0 272)')
      await expect(page.locator('.workspace-search-empty > div > span')).toHaveCSS('padding-inline', '0px')
      await expect(page.locator('.workspace-search-results')).toHaveCount(0)
      await expect(page.locator('.bottom-agent')).toBeHidden()
      await expect(page.locator('.workspace-search-header [data-sidebar-trigger] path')).toHaveAttribute('d', /^M4.25 2C2.45508/)
      await expect(page.locator('.workspace-search-field svg path')).toHaveAttribute('d', /^M7 2C9.76142/)
      await expect(tabs.locator('.workspace-search-more-tabs path')).toHaveAttribute('d', /^M10.1611 0.314094/)
      await geometry(tabs.locator('.workspace-search-more-tabs svg'), { width: 8, height: 9 })
      await noOverflow(page)
      if (shots) await page.screenshot({ path: `${shots}/search-${viewport.width}.png` })

      await page.getByRole('button', { name: 'Add filter', exact: true }).tap()
      const filters = page.getByRole('dialog', { name: 'Search filters', exact: true })
      await geometry(filters, { x: 16, width: viewport.width - 32 })
      await filters.getByRole('option', { name: 'Status type', exact: true }).tap()
      const statusPanel = page.locator('.phone-menu-surface').last()
      await geometry(statusPanel, { x: 16, width: viewport.width - 32 })
      await expect(filters).not.toBeVisible()
      await expect(statusPanel.locator('input:visible')).toHaveCount(1)
      await statusPanel.locator('input:visible').fill('Completed')
      await expect(statusPanel.getByRole('option')).toHaveCount(1)
      await statusPanel.getByRole('option', { name: 'Completed', exact: true }).tap()
      await expect(page.locator('.workspace-search-filter-chips')).toContainText('Completed')
      await expect(filters).not.toBeVisible()
      await page.getByRole('button', { name: 'Display options', exact: true }).tap()
      await geometry(page.getByRole('dialog', { name: 'Search display options', exact: true }), { x: 16, width: viewport.width - 32 })
      await page.keyboard.press('Escape')
    })

    test('Help closes the drawer and opens a compact searchable support panel', async ({ page, workspaceKey }) => {
      await page.context().route('https://github.com/**', route => route.fulfill({ contentType: 'text/html', body: '<title>Support request</title>' }))
      await page.goto(`${base}/${workspaceKey}/my-issues/assigned`)
      await page.locator('[data-sidebar-trigger]').tap()
      await page.getByRole('button', { name: 'Open Help menu', exact: true }).tap()
      const menu = page.getByRole('dialog', { name: 'Help', exact: true })
      await geometry(menu, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32, height: 450 })
      await expect(page.locator('.sidebar-scrim')).not.toHaveClass(/open/)
      await expect(page.locator('.bottom-agent')).toBeHidden()
      await expect(menu.locator('.phone-menu-context')).toHaveText('Help')
      await expect(menu.getByRole('option')).toHaveCount(9)
      for (const name of ['Docs', 'Contact us', 'Send flare', 'Keyboard shortcuts', 'Flow status', 'Download apps', 'Settings', 'Slack community']) {
        const row = menu.getByRole('option', { name, exact: true })
        await geometry(row, { x: 22.5, width: viewport.width - 44, height: 46 })
        await expect(row.locator('span')).toHaveCSS('font-size', '13px')
        await expect(row.locator('span')).toHaveCSS('font-weight', '450')
        await expect(row.locator('span')).toHaveCSS('line-height', '15.6px')
      }
      await noOverflow(page)
      if (shots) await page.screenshot({ path: `${shots}/help-sheet-${viewport.width}.png` })
      const filter = menu.getByRole('combobox', { name: 'Help with...', exact: true })
      await filter.fill('not a support row')
      await expect(menu.getByRole('option')).toHaveCount(0)
      await expect(menu.getByText('No results', { exact: true })).toBeVisible()
      await filter.fill('Send flare')
      await expect(menu.getByRole('option')).toHaveCount(1)
      const popupPromise = page.waitForEvent('popup')
      await menu.getByRole('option', { name: 'Send flare', exact: true }).tap()
      const popup = await popupPromise
      expect(new URL(popup.url()).pathname).toContain('issues/new')
      expect(new URL(popup.url()).searchParams.get('title')).toBe('Support diagnostics')
      expect(new URL(popup.url()).searchParams.get('body')).toContain(`Page: /${workspaceKey}/my-issues/assigned`)
      await popup.close()
      await expect(menu).toHaveCount(0)
      await page.locator('[data-sidebar-trigger]').tap()
      await page.getByRole('button', { name: 'Open Help menu', exact: true }).tap()
      await expect(menu.getByRole('combobox')).toHaveValue('')
      await menu.getByRole('combobox').fill('Keyboard')
      await menu.getByRole('option', { name: 'Keyboard shortcuts', exact: true }).tap()
      await expect(menu).toHaveCount(0)
      await expect(page.getByRole('dialog', { name: 'Keyboard Shortcuts', exact: true })).toBeVisible()
    })

    test('workspace panel keeps compact labels, both dividers and a working filter', async ({ page, workspaceKey }) => {
      await page.goto(`${base}/${workspaceKey}/my-issues/assigned`)
      await page.locator('[data-sidebar-trigger]').tap()
      await expect(page.locator('.sidebar-primary-links a[href$="/inbox"] svg path')).toHaveAttribute('d', /^M11.0069 1.00879/)
      await page.locator('.workspace-menu-trigger').tap()
      const menu = page.getByRole('dialog', { name: 'Workspace Menu', exact: true })
      await geometry(menu, { x: 16, y: Math.min(110, viewport.height * .1304), width: viewport.width - 32, height: 312 })
      await expect(menu.getByRole('option')).toHaveCount(5)
      await expect(page.locator('.workspace-menu-trigger')).toHaveAttribute('aria-expanded', 'true')
      await expect(page.locator('.workspace-menu-trigger')).toHaveCSS('background-color', 'lch(11.37 1.3 272)')
      const settings = menu.getByRole('option', { name: 'Settings', exact: true })
      await expect(settings).toHaveAttribute('data-selected', 'true')
      await expect.poll(() => settings.evaluate(e => getComputedStyle(e, '::before').backgroundColor)).toBe('lch(20.82 1.3 272)')
      await page.keyboard.press('ArrowDown')
      const invite = menu.getByRole('option', { name: 'Invite and manage members', exact: true })
      await expect(invite).toHaveAttribute('data-selected', 'true')
      await expect(settings).toHaveAttribute('data-selected', 'false')
      await expect.poll(() => invite.evaluate(e => getComputedStyle(e, '::before').backgroundColor)).toBe('lch(20.82 1.3 272)')
      await page.keyboard.press('ArrowUp')
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
      await expect(picker.getByRole('option').first()).toHaveCSS('font-size', '13px')
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
      await expect(drawer).toHaveAttribute('aria-hidden', 'true')
      await page.locator('[data-sidebar-trigger]').first().click()
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
    await geometry(page.getByRole('dialog', { name: 'Add filter' }), { x: 16, width: 358 })
    await page.getByRole('option', { name: /Status/ }).first().click()
    await geometry(page.locator('[role="dialog"][data-field="status"]'), { x: 16, width: 358 })
    await page.keyboard.press('Escape')
    await page.mouse.click(200, 22)
    await expect(page.getByRole('dialog', { name: 'Add Filter…' })).not.toBeVisible()
    if (shots) await page.screenshot({ path: `${shots}/flow-myissues-390.png` })
  })
})

test.describe('inbox phone top bar', () => {
  test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent })
  test('matches the single action row and notification list', async ({ page, request, workspaceKey }) => {
    const headers = { 'X-Workspace-Key': workspaceKey }
    const bootstrap = await (await request.get(`${api}/api/bootstrap`, { headers })).json() as {
      teams: Array<{ id: string }>; states: Array<{ id: string; type: string }>
    }
    const issue = await request.post(`${api}/api/issues`, {
      headers,
      data: { title: '[mobile] Inbox top bar', description: '', teamId: bootstrap.teams[0].id, stateId: bootstrap.states[0].id },
    })
    expect(issue.status()).toBe(201)
    const { id } = await issue.json() as { id: string }
    const reminder = await request.post(`${api}/api/issues/${id}/reminders`, {
      headers,
      data: { remindAt: new Date(Date.now() + 500).toISOString() },
    })
    expect(reminder.status()).toBe(201)
    await page.waitForTimeout(900)

    await page.goto(`${base}/${workspaceKey}/inbox`)
    await geometry(page.locator('.flow-inbox__header-wrapper'), { x: 0, y: 0, width: 390, height: 44 })
    await geometry(page.locator('.flow-inbox__header-inner'), { x: 8, y: 0, width: 374, height: 43.5 })
    await geometry(page.locator('.flow-inbox__header-controls button').first(), { x: 274, y: 5.75, width: 32, height: 32 })
    await geometry(page.locator('.flow-inbox-notification-list > [role="listitem"] .flow-inbox-row').first(), { x: 0, y: 52, width: 390, height: 55 })
    await expect(page.locator('.bottom-agent')).toBeHidden()
    await noOverflow(page)

    await page.getByRole('button', { name: 'Add filter', exact: true }).click()
    await geometry(page.getByRole('dialog', { name: 'Add filter' }), { x: 16, width: 358 })
    await page.keyboard.press('Escape')
    if (shots) await page.screenshot({ path: `${shots}/flow-inbox-topbar-390.png` })
  })
})

test.describe('team issues phone toolbar', () => {
  test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent })
  test('matches the team breadcrumb, selected all-issues tab and grouped toolbar', async ({ page, request, workspaceKey }) => {
    const headers = { 'X-Workspace-Key': workspaceKey }
    const bootstrap = await (await request.get(`${api}/api/bootstrap`, { headers })).json() as {
      teams: Array<{ id: string; key: string }>; states: Array<{ id: string; type: string }>
    }
    const team = bootstrap.teams[0]
    const started = bootstrap.states.find(state => state.type === 'started')!.id
    const todo = bootstrap.states.find(state => state.type === 'unstarted')!.id
    for (let index = 1; index <= 7; index += 1) {
      const response = await request.post(`${api}/api/issues`, { headers, data: { title: `[mobile] Team started ${index}`, teamId: team.id, stateId: started } })
      expect(response.status()).toBe(201)
    }
    for (let index = 1; index <= 10; index += 1) {
      const response = await request.post(`${api}/api/issues`, { headers, data: { title: `[mobile] Team todo ${index}`, teamId: team.id, stateId: todo } })
      expect(response.status()).toBe(201)
    }
    const defaults = { grouping: 'status', groupOrder: 'desc', ordering: 'importance', layout: 'list', properties: ['id', 'status', 'assignee', 'priority', 'project', 'estimate', 'labels', 'created'], completedWindow: 'all', showTriageIssues: false, showSubIssues: true, nestedSubIssues: false, showEmptyGroups: false }
    expect((await request.patch(`${api}/api/teams/${team.id}/settings`, { headers, data: { issueViewDefaults: { all: defaults } } })).ok()).toBe(true)
    expect((await request.patch(`${api}/api/workspaces/${workspaceKey}/teams/${team.id}`, { headers, data: { name: '[mobile] This is a deliberately long team name' } })).ok()).toBe(true)

    await page.goto(`${base}/${workspaceKey}/team/${team.key}/all`)
    await geometry(page.locator('[data-issue-explorer] [data-content-view-header]'), { x: 0, y: 0, width: 390, height: 44 })
    await geometry(page.locator('[data-issue-explorer] [data-content-view-subheader]'), { x: 0, y: 44, width: 390, height: 43.5 })
    await expect(page.locator('[data-content-view-breadcrumb] [data-current]')).toHaveText('Issues')
    const breadcrumb = page.locator('[data-content-view-breadcrumb]')
    const favorite = page.locator('[data-content-view-header] button[role="switch"]')
    await expect.poll(async () => {
      const breadcrumbBox = await breadcrumb.boundingBox()
      const favoriteBox = await favorite.boundingBox()
      return breadcrumbBox && favoriteBox ? favoriteBox.x - (breadcrumbBox.x + breadcrumbBox.width) : -1
    }).toBeGreaterThanOrEqual(0)
    await expect.poll(async () => {
      const breadcrumbBox = await breadcrumb.boundingBox()
      return breadcrumbBox ? breadcrumbBox.width : 0
    }).toBeLessThan(250)
    await expect(page.locator('[data-content-view-subheader] nav a[aria-current="page"]')).toHaveText('All issues')
    await geometry(page.locator('[data-my-issues-group-header]').first(), { x: 8, y: 87.5, width: 374, height: 36 })
    await expect(page.locator('[data-my-issues-group-title]').first()).toHaveText('In Progress')
    await expect(page.locator('[data-my-issues-group-header]').first().locator('[class*="groupCount"]')).toHaveText('7')
    await expect(page.locator('[data-issue-display-trigger] span[aria-hidden="true"]')).toBeVisible()
    await expect(page.locator('.bottom-agent')).toBeHidden()
    await noOverflow(page)
    if (shots) await page.screenshot({ path: `${shots}/flow-team-topbar-390.png` })
  })
})

test.describe('phone command menu', () => {
  test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent })
  test('opens the selected issue actions at the phone-sheet geometry', async ({ page, request, workspaceKey }) => {
    const headers = { 'X-Workspace-Key': workspaceKey }
    const bootstrap = await (await request.get(`${api}/api/bootstrap`, { headers })).json() as {
      viewer: { id: string }; teams: Array<{ id: string }>; states: Array<{ id: string; type: string }>
    }
    const stateId = (bootstrap.states.find(state => state.type === 'started') ?? bootstrap.states[0]).id
    const issue = await request.post(`${api}/api/issues`, {
      headers,
      data: { title: '[mobile] Command menu parity', description: '', teamId: bootstrap.teams[0].id, stateId, assigneeId: bootstrap.viewer.id },
    })
    expect(issue.status()).toBe(201)

    await page.goto(`${base}/${workspaceKey}/my-issues/assigned`)
    await page.locator('[data-issue-row-container] [role="checkbox"]').first().click()
    await page.keyboard.press('Meta+k')
    const menu = page.locator('.command-dialog')
    await expect(menu).toBeVisible()
    await geometry(menu, { x: 16, y: 110, width: 358, height: 450 })
    for (const label of ['Assign to…', 'Un-assign from me', 'Change status…', 'Set priority…', 'Add to project…', 'Add labels…', 'Move to a different team…', 'Set due date…']) {
      await expect(menu.getByRole('option').filter({ hasText: label.replace('…', '') })).toBeVisible()
    }
    await expect(page.locator('[data-my-issues-bulk-bar]')).toBeHidden()
    await noOverflow(page)
    if (shots) await page.screenshot({ path: `${shots}/flow-command-menu-390.png` })
    await page.keyboard.press('Escape')
    await expect(menu).not.toBeVisible()
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
