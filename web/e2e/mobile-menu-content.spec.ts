import { expect, test, type Page } from '@playwright/test'

const userAgent = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1'
const base = process.env.FLOW_MOBILE_BASE_URL ?? 'http://127.0.0.1:4173'

test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent })

function trackPageErrors(page: Page) {
  const errors: string[] = []
  page.on('pageerror', error => errors.push(error.message))
  return errors
}

test.describe('phone menus with custom content', () => {
  test('search filter keeps its status controls usable', async ({ page }) => {
    const errors = trackPageErrors(page)
    await page.goto(`${base}/verify/search`)
    await page.getByRole('button', { name: 'Add filter', exact: true }).click()
    const menu = page.locator('.workspace-search-menu')
    await expect(menu).toBeVisible()
    await expect(page.locator('.phone-menu-surface')).toHaveCount(0)

    await page.getByRole('menuitem', { name: /Status type/ }).click()
    const values = page.locator('.workspace-search-value-menu')
    await expect(values).toBeVisible()
    await expect(values.getByRole('option')).toHaveCount(5)
    await values.getByRole('option', { name: 'Started', exact: true }).click()

    await expect(page.locator('.workspace-search-filter-chips')).toContainText('Status type')
    await expect(page.locator('.workspace-search-filter-chips')).toContainText('Started')
    await expect(menu).not.toBeVisible()
    expect(errors).toEqual([])
  })

  test('workspace member filter uses its radio group without crashing', async ({ page }) => {
    const errors = trackPageErrors(page)
    await page.goto(`${base}/verify/settings/members`)
    await page.locator('.settings-members-filter').click()
    const menu = page.locator('.settings-members-filter-menu')
    await expect(menu).toBeVisible()
    await expect(page.locator('.phone-menu-surface')).toHaveCount(0)
    await expect(menu.getByRole('menuitemradio')).toHaveCount(8)

    await menu.getByRole('menuitemradio', { name: 'Suspended', exact: true }).click()
    await expect(page.locator('.settings-members-filter')).toHaveText(/Suspended/)
    await expect(menu).not.toBeVisible()
    expect(errors).toEqual([])
  })

  test('loop trigger filter input types without selecting a hidden row', async ({ page }) => {
    const errors = trackPageErrors(page)
    await page.goto(`${base}/verify/loops/my-loops`)
    await page.getByRole('button', { name: 'New loop' }).first().click()
    await page.getByRole('button', { name: 'Start from scratch' }).click()
    await page.getByRole('option', { name: /^Workspace/ }).first().click()
    const name = page.getByRole('textbox', { name: 'Loop name' })
    await expect(name).toBeVisible()
    await name.fill('[mobile] trigger menu check')

    await page.getByRole('button', { name: 'Trigger', exact: true }).click()
    await expect(page.locator('.loops-trigger-menu')).toBeVisible()
    await expect(page.locator('.phone-menu-surface')).toHaveCount(0)

    await page.getByRole('menuitem', { name: 'Issue', exact: true }).click()
    const events = page.locator('.loops-issue-events')
    await expect(events).toBeVisible()
    const filter = events.getByRole('textbox', { name: 'Filter…' })
    await filter.fill('tri')
    await expect(events.getByRole('menuitem')).toHaveCount(1)
    await expect(events.getByRole('menuitem', { name: 'Is in triage', exact: true })).toBeVisible()
    await filter.press('Enter')
    await expect(events).toBeVisible()
    await events.getByRole('menuitem', { name: 'Is in triage', exact: true }).click()

    await expect(page.getByRole('button', { name: 'Trigger', exact: true })).toContainText('An issue is in triage')
    expect(errors).toEqual([])
  })
})
