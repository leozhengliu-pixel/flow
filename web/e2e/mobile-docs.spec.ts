import { expect, test, type APIRequestContext } from '@playwright/test'

const API = 'http://127.0.0.1:4180'

function advancedFilterParam() {
  const filters = [{ id: 'advanced_ref', field: 'advanced', fieldLabel: 'Advanced filter', operator: 'is', value: '', valueLabel: '', values: [], tree: { id: 'advanced_ref-root', conjunction: 'and', items: [] } }]
  return Buffer.from(JSON.stringify(filters), 'utf8').toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

async function seedTeamDocuments(request: APIRequestContext, workspaceKey: string, label: string) {
  const created = await request.post(`${API}/api/workspaces`, {
    data: { name: 'Mobile documents parity', urlKey: workspaceKey, region: 'us', tutorial: false },
  })
  expect(created.status()).toBe(201)
  const bootstrap = await created.json() as { teams: { id: string; key: string }[] }
  const team = bootstrap.teams[0]
  const headers = { 'X-Workspace-Key': workspaceKey }
  expect((await request.patch(`${API}/api/workspaces/${workspaceKey}/teams/${team.id}`, { data: { name: label }, headers })).ok()).toBe(true)
  expect((await request.patch(`${API}/api/workspace/preferences`, { data: { featureFlags: { loops: false } }, headers })).ok()).toBe(true)
  const projectResponse = await request.post(`${API}/api/projects`, { data: { name: '[mobile] Compare Test', teamIds: [team.id] }, headers })
  expect(projectResponse.status()).toBe(201)
  const project = await projectResponse.json() as { id: string }
  for (const document of [{ teamIds: [team.id] }, { teamIds: [team.id], projectIds: [project.id] }, { teamIds: [team.id], projectIds: [project.id] }]) {
    expect((await request.post(`${API}/api/documents`, { data: { title: '[mobile] New document', ...document }, headers })).status()).toBe(201)
  }
  return team
}

test.describe('mobile documents list', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, colorScheme: 'dark' })

  test('phone Documents tab rows show group headers, checkboxes, document icons and titles', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-docs-${testInfo.project.name}-${testInfo.retry}`
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

  test('selecting a document shows the floating bulk action pill', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-docs-selected-${testInfo.project.name}-${testInfo.retry}`
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

  test('active Documents tab collapses into a dropdown listing Documents and Members', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-docs-tabs-${testInfo.project.name}-${testInfo.retry}`
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

  test('the round filter button opens the full-width filter command menu', async ({ page, request }, testInfo) => {
    const workspaceKey = `mobile-docs-filter-${testInfo.project.name}-${testInfo.retry}`
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
