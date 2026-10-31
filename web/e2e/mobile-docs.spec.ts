import { expect, type APIRequestContext } from '@playwright/test'
import { test } from './workspace-fixture'

test.describe('mobile document options', () => {
  test.use({ isMobile: true, hasTouch: true, colorScheme: 'dark' })

  for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
    test(`document commands filter, scroll and open submenus at ${viewport.width}px`, async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Flow-Workspace': workspaceKey }
      const response = await request.post('/api/documents', { headers, data: { title: '[mobile] Document options', content: '[mobile] Body' } })
      expect(response.status()).toBe(201)
      const document = await response.json() as { slugId: string }
      await page.setViewportSize(viewport)
      await page.goto(`/${workspaceKey}/document/${document.slugId}`)
      const trigger = page.getByRole('button', { name: 'Document options', exact: true })
      await trigger.click()
      const menu = page.getByRole('menu', { name: 'Document options', exact: true })
      const search = menu.getByRole('textbox', { name: 'Command menu', exact: true })
      await expect(search).toBeVisible()
      await menu.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
      const rows = menu.locator(':scope > .document-page-options-list > [data-menu-item]')
      expect(await rows.evaluateAll(items => items.map(item => item.getAttribute('data-menu-item')))).toEqual(['Move to', 'Pin to team', 'Duplicate', 'Rename…', 'New template from document', 'Favorite', 'Copy', 'Download', 'Remind me', 'Show document history', 'Delete'])
      const rect = await menu.boundingBox()
      expect(rect?.x).toBe(16)
      expect(rect?.width).toBe(viewport.width - 32)
      expect(rect?.height).toBe(450)
      expect(Math.abs((rect?.y ?? 0) - viewport.height * .13)).toBeLessThan(1)
      expect((await rows.first().boundingBox())?.height).toBe(46)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      await expect(menu.getByRole('separator')).toHaveCount(2)
      await search.fill('no matching command')
      await expect(menu.getByText('No results', { exact: true })).toBeVisible()
      await search.fill('history')
      await expect(rows).toHaveCount(1)
      await expect(rows.first()).toContainText('Show document history')
      await page.keyboard.press('Escape')
      await expect(menu).not.toBeVisible()
      await trigger.click()
      await expect(search).toHaveValue('')
      await expect(rows).toHaveCount(11)
      await rows.last().scrollIntoViewIfNeeded()
      await expect(rows.last()).toBeInViewport()
      await search.fill('Download')
      await search.press('ArrowDown')
      await expect(menu.getByRole('menuitem', { name: 'Download', exact: true })).toBeFocused()
      await page.keyboard.press('Enter')
      await expect(page.getByRole('menuitem', { name: 'Markdown', exact: true })).toBeVisible()
      await page.keyboard.press('Escape')
      await page.keyboard.press('Escape')
    })
  }
})

test.describe('mobile document view', () => {
  test.use({ isMobile: true, hasTouch: true, colorScheme: 'dark' })

  for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
    test(`short project document retains its header and editable body at ${viewport.width}px`, async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const bootstrap = await (await request.get('/api/bootstrap', { headers })).json() as { teams: { id: string }[] }
      const project = await (await request.post('/api/projects', { headers, data: { name: '[mobile] Compare Test', teamIds: [bootstrap.teams[0].id] } })).json() as { id: string }
      const response = await request.post('/api/documents', { headers, data: { title: '123', content: '123123123', color: '#bec2c8', projectIds: [project.id] } })
      expect(response.status()).toBe(201)
      const document = await response.json() as { slugId: string }
      await page.setViewportSize(viewport)
      await page.goto(`/${workspaceKey}/document/${document.slugId}`)

      await expect(page.getByRole('textbox', { name: 'Document title', exact: true })).toHaveValue('123')
      await expect(page.getByRole('textbox', { name: 'Document content', exact: true })).toContainText('123123123')
      await expect(page.getByRole('link', { name: '[mobile] Compare Test' })).toBeVisible()
      await expect(page.locator('.document-edited__label--phone')).toBeVisible()
      expect((await page.locator('.document-title').boundingBox())?.y).toBe(150)
      expect((await page.locator('.document-editor .flow-prosemirror').boundingBox())?.x).toBe(-2)
      for (const selector of ['.document-mobile-menu', '.document-title-actions button[role="switch"]', '.document-title-actions button[aria-label="Document options"]', '.document-edited', '.document-header-actions button[aria-label="Copy document URL"]', '.document-header-actions button[aria-label="Change document subscribers"]']) {
        const bounds = await page.locator(selector).boundingBox()
        expect(bounds?.height).toBe(32)
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)

      await page.locator('.document-edited').click()
      await expect(page.locator('.document-owner-row')).toBeVisible()
      await page.keyboard.press('Escape')
      await page.getByRole('textbox', { name: 'Document title', exact: true }).fill('[mobile] Updated title')
      await page.getByRole('textbox', { name: 'Document title', exact: true }).blur()
      await expect(page.locator('.document-breadcrumb-current')).toContainText('[mobile] Updated title')
    })
  }
})

function advancedFilterParam() {
  const filters = [{ id: 'advanced_ref', field: 'advanced', fieldLabel: 'Advanced filter', operator: 'is', value: '', valueLabel: '', values: [], tree: { id: 'advanced_ref-root', conjunction: 'and', items: [] } }]
  return Buffer.from(JSON.stringify(filters), 'utf8').toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

async function seedTeamDocuments(request: APIRequestContext, workspaceKey: string, label: string) {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const response = await request.get('/api/bootstrap', { headers })
  expect(response.ok()).toBeTruthy()
  const bootstrap = await response.json() as { teams: { id: string; key: string }[] }
  const team = bootstrap.teams[0]
  expect((await request.patch(`/api/workspaces/${workspaceKey}/teams/${team.id}`, { data: { name: label }, headers })).ok()).toBe(true)
  expect((await request.patch('/api/workspace/preferences', { data: { featureFlags: { loops: false } }, headers })).ok()).toBe(true)
  const projectResponse = await request.post('/api/projects', { data: { name: '[mobile] Compare Test', teamIds: [team.id] }, headers })
  expect(projectResponse.status()).toBe(201)
  const project = await projectResponse.json() as { id: string }
  for (const document of [{ teamIds: [team.id] }, { teamIds: [team.id], projectIds: [project.id] }, { teamIds: [team.id], projectIds: [project.id] }]) {
    expect((await request.post('/api/documents', { data: { title: '[mobile] New document', ...document }, headers })).status()).toBe(201)
  }
  return team
}

test.describe('mobile documents list', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('phone Documents tab rows show group headers, checkboxes, document icons and titles', async ({ page, request, workspaceKey }, testInfo) => {
    const team = await seedTeamDocuments(request, workspaceKey, '[mobile] Docs')

    await page.goto(`/${workspaceKey}/team/${team.key}/documents?doc-filter=${advancedFilterParam()}`)
    await expect(page.locator('.team-home-topbar')).toHaveCSS('height', '44px')
    await expect(page.locator('.team-home-band')).toHaveCSS('height', '43.5px')
    await expect(page.locator('.team-home-tabs a[aria-current="page"]')).toContainText('Documents')
    await page.locator('.team-home-tabs a[aria-current="page"]').click()
    await expect(page.getByRole('menu', { name: 'Team views' })).toHaveCount(0)
    expect((await page.locator('.team-documents-new').boundingBox())?.width).toBe(32)
    await expect(page.locator('.team-documents-filter-bar [data-advanced-chip]')).toContainText('Advanced filter')
    await expect(page.locator('.team-documents-filter-bar > div:last-child > button')).toContainText('Clear')

    const groups = page.locator('.team-docs-group')
    expect(await groups.count()).toBe(2)
    expect((await groups.first().boundingBox())?.height).toBe(36)
    await expect(groups.first()).toContainText('Team documents')
    await expect(groups.first().locator('small')).toHaveText('1')
    await expect(groups.nth(1)).toContainText('[mobile] Compare Test')
    await expect(groups.nth(1).locator('small')).toHaveText('2')

    const rows = page.locator('.team-docs-row')
    expect(await rows.count()).toBe(3)
    expect((await rows.first().boundingBox())?.height).toBe(48)
    expect((await rows.first().boundingBox())?.width).toBe(390)
    await expect(rows.first().locator('.team-docs-title strong')).toHaveText('[mobile] New document')
    expect((await rows.first().locator('.team-docs-check span').boundingBox())?.width).toBe(14)
    const icon = rows.first().locator('.team-docs-title > svg')
    expect((await icon.boundingBox())?.width).toBe(16)
    await expect(icon).toHaveCSS('color', 'rgb(255, 255, 255)')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/documents-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })
})

test.describe('mobile documents selection', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('selecting a document shows the floating bulk action pill', async ({ page, request, workspaceKey }, testInfo) => {
    const team = await seedTeamDocuments(request, workspaceKey, '[mobile] Docs selected')

    await page.goto(`/${workspaceKey}/team/${team.key}/documents?doc-filter=${advancedFilterParam()}`)
    const row = page.locator('.team-docs-row').nth(1)
    await row.locator('input[aria-label="Select document"]').check()
    await expect(row).toHaveAttribute('data-selected', 'true')
    await expect(row.locator('.team-docs-check span')).toHaveCSS('width', '14px')

    const bar = page.locator('.team-docs-selection-bar')
    await expect(bar).toHaveCSS('height', '48px')
    const pill = page.locator('.team-docs-selection')
    expect((await pill.boundingBox())?.height).toBe(48)
    await expect(pill.locator('> span')).toHaveText('1 selected')
    const actions = page.locator('.team-docs-selection-actions')
    expect((await actions.boundingBox())?.height).toBe(32)
    await expect(actions).toContainText('Actions')
    await expect(page.locator('.team-docs-selection-agent')).toBeHidden()
    expect((await page.locator('.team-docs-selection-clear').boundingBox())?.width).toBe(32)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/documents-selected-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })

    await page.locator('.team-docs-selection-clear').click()
    await expect(page.locator('.team-docs-selection')).toHaveCount(0)
    await expect(row).toHaveAttribute('data-selected', 'false')
  })
})

test.describe('mobile documents tab menu', () => {
  test.use({ viewport: { width: 375, height: 667 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('active Documents tab collapses into a dropdown listing Documents and Members', async ({ page, request, workspaceKey }) => {
    const team = await seedTeamDocuments(request, workspaceKey, '[mobile] Docs tabs')

    await page.goto(`/${workspaceKey}/team/${team.key}/documents?doc-filter=${advancedFilterParam()}`)
    const trigger = page.locator('[data-team-tab-active]')
    await expect(trigger).toContainText('Documents')
    await expect(trigger).toHaveAttribute('aria-expanded', 'false')
    await expect(page.locator('.team-home-tabs a').filter({ hasText: 'Members' })).toHaveCount(0)
    await expect(page.locator('.team-home-tabs a').filter({ hasText: 'Overview' })).toBeVisible()

    await trigger.click()
    const menu = page.getByRole('menu', { name: 'Team views' })
    await expect(menu).toBeVisible()
    await expect(menu.getByRole('menuitem')).toHaveCount(2)
    await expect(menu.getByRole('menuitem', { name: 'Documents' })).toHaveAttribute('data-active', 'true')
    await expect(menu.getByRole('menuitem', { name: 'Members' })).not.toHaveAttribute('data-active', 'true')

    await menu.getByRole('menuitem', { name: 'Members' }).click()
    await expect(page).toHaveURL(new RegExp(`/${team.key}/members$`))
    await expect(page.locator('.team-home-tabs a[aria-current="page"]')).toContainText('Members')
  })
})

test.describe('mobile documents filter menu', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('the round filter button opens the full-width filter command menu', async ({ page, request, workspaceKey }, testInfo) => {
    const team = await seedTeamDocuments(request, workspaceKey, '[mobile] Docs filter')

    await page.goto(`/${workspaceKey}/team/${team.key}/documents?doc-filter=${advancedFilterParam()}`)
    await page.locator('.team-documents-toolbar button[aria-label="Add filter"]').click()

    const menu = page.locator('[data-radix-popper-content-wrapper] > [data-layout="documents"]')
    await expect(menu).toBeVisible()
    await page.waitForTimeout(300)
    const box = await menu.boundingBox()
    expect(box?.x).toBe(16)
    expect(box?.width).toBe(358)
    expect(Math.abs((box?.y ?? 0) - 110)).toBeLessThanOrEqual(1)
    expect(Math.abs((box?.height ?? 0) - 312)).toBeLessThanOrEqual(1)

    const search = menu.locator('input')
    await expect(search).toHaveAttribute('placeholder', 'Add Filter…')
    expect((await search.boundingBox())?.height).toBe(40)
    expect((await search.boundingBox())?.x).toBe(22.5)

    const rows = menu.locator('[cmdk-item]')
    expect(await rows.count()).toBe(5)
    for (const [index, label] of ['Advanced filter', 'Creator', 'Owner', 'Project', 'Dates'].entries()) {
      await expect(rows.nth(index)).toContainText(label)
      expect((await rows.nth(index).boundingBox())?.height).toBe(46)
    }
    expect((await rows.first().locator('span').nth(1).boundingBox())?.x).toBe(58.5)

    const separators = menu.locator('[cmdk-separator]')
    expect(await separators.count()).toBe(2)
    for (let index = 0; index < 2; index++) {
      expect((await separators.nth(index).boundingBox())?.x).toBe(16.5)
      expect((await separators.nth(index).boundingBox())?.width).toBe(358)
    }

    const trigger = page.locator('.team-documents-toolbar button[aria-label="Add filter"]')
    await expect(trigger).toHaveAttribute('aria-expanded', 'true')
    await expect(trigger).toHaveCSS('background-color', 'lch(18.634 1.075 272)')
    await expect(trigger.locator('svg')).toHaveCSS('color', 'lch(100 0 272)')

    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/documents-filter-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })
})

test.describe('mobile documents display options', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('the rightmost toolbar button opens the compact display popover', async ({ page, request, workspaceKey }, testInfo) => {
    const team = await seedTeamDocuments(request, workspaceKey, '[mobile] Docs display')

    await page.goto(`/${workspaceKey}/team/${team.key}/documents?doc-filter=${advancedFilterParam()}`)
    const trigger = page.locator('.team-documents-toolbar button[aria-label="Display options"]')
    await trigger.click()

    const popover = page.locator('[data-list-display]')
    await expect(popover).toBeVisible()
    await page.waitForTimeout(250)
    const box = await popover.boundingBox()
    await expect(popover).toHaveCSS('width', '301px')
    expect(Math.abs((box?.height ?? 0) - 244)).toBeLessThanOrEqual(1)
    expect(Math.abs((box?.x ?? 0) - 81)).toBeLessThanOrEqual(1)
    expect(Math.abs((box?.y ?? 0) - 85.67)).toBeLessThanOrEqual(1)
    await expect(popover).toHaveCSS('border-radius', '8px')

    const rows = popover.locator('section').first().locator('> div')
    expect(await rows.count()).toBe(4)
    for (let index = 0; index < 4; index++) expect((await rows.nth(index).boundingBox())?.height).toBeCloseTo(36, 2)

    const grouping = popover.locator('section').first().locator('[role="combobox"]')
    expect(await grouping.count()).toBe(2)
    for (let index = 0; index < 2; index++) {
      expect((await grouping.nth(index).boundingBox())?.width).toBe(102)
      expect((await grouping.nth(index).boundingBox())?.height).toBe(28)
    }
    expect((await popover.locator('[data-sort-direction]').boundingBox())?.width).toBe(28)

    const toggles = popover.locator('section').first().locator('input[type="checkbox"]')
    expect(await toggles.count()).toBe(2)
    for (let index = 0; index < 2; index++) {
      expect((await toggles.nth(index).boundingBox())?.width).toBe(22)
      expect((await toggles.nth(index).boundingBox())?.height).toBe(14)
    }

    const chips = popover.locator('section').nth(1).locator('button')
    expect(await chips.count()).toBe(3)
    for (let index = 0; index < 3; index++) expect((await chips.nth(index).boundingBox())?.height).toBe(28)
    await expect(chips.first()).toHaveCSS('background-color', 'lch(25.834 1.525 272)')

    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/documents-display-${testInfo.project.name}.png`, scale: 'css', animations: 'disabled' })
  })
})
