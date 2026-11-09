import { test, expect } from './issue-list-fixture'
import type { SavedView } from '../src/types/flow'

test.skip(({ isMobile }) => isMobile, 'Desktop saved-view details')
test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark', deviceScaleFactor: 2, launchOptions: { args: ['--force-device-scale-factor=2'] } })

for (const colorScheme of ['light', 'dark'] as const) {
  test(`draws each saved view details card with one real border and the shared card shadow (${colorScheme})`, async ({ page, request, workspaceKey, issueList }) => {
    expect(issueList.issues).toHaveLength(3)
    await page.emulateMedia({ colorScheme })
    const response = await request.post('/api/views', {
      headers: { 'X-Workspace-Key': workspaceKey },
      data: { name: '[audit] Border queue', resource: 'issues', scope: 'personal', view: 'all', filters: [], display: { grouping: 'status' } },
    })
    const view = await response.json() as SavedView
    await page.goto(`/${workspaceKey}/view/${view.slugId || view.id}`)
    const panel = page.getByRole('complementary', { name: 'View sidebar' })
    await expect(panel).toBeVisible()
    await expect(panel.getByRole('button', { name: 'No assignee 2', exact: true })).toBeVisible()
    const measured = await page.evaluate(() => {
      const aside = document.querySelector('[aria-label="View sidebar"]')!
      const token = document.createElement('div')
      token.style.boxShadow = 'var(--shadow)'
      document.body.append(token)
      const shadow = getComputedStyle(token).boxShadow
      token.remove()
      const rect = (element: Element) => { const box = element.getBoundingClientRect(); return [box.x, box.y, box.width, box.height] }
      const cards = [...aside.querySelectorAll('section')].map(card => { const style = getComputedStyle(card); return { rect: rect(card), width: style.borderTopWidth, borderStyle: style.borderTopStyle, sides: [style.borderRightWidth, style.borderBottomWidth, style.borderLeftWidth], radius: style.borderTopLeftRadius, shadow: style.boxShadow, padding: style.paddingLeft } })
      const tabs = [...aside.querySelectorAll('[role="tab"]')].map(rect)
      const rows = [...aside.querySelectorAll('[role="tabpanel"] > button')].map(rect)
      return { aside: rect(aside), cards, tabs, rows, shadow, main: rect(document.querySelector('main')!) }
    })
    expect(measured.cards).toHaveLength(3)
    for (const card of measured.cards) {
      // A real 0.5px border (no inset ring beside a themed drop shadow, which drew a second outline) and the content card's shadow.
      expect(card).toMatchObject({ width: '0.5px', borderStyle: 'solid', sides: ['0.5px', '0.5px', '0.5px'], radius: '10px', shadow: measured.shadow, padding: '12px' })
      expect(card.shadow).not.toContain('inset')
      expect(card.rect[0]).toBe(measured.aside[0] + 4)
      expect(card.rect[2]).toBe(388)
    }
    // Flush with the content card's right edge minus its 8px gutter; cards 8px apart; the summary card fills the rest of the column.
    expect(measured.cards[0].rect[1] + measured.cards[0].rect[3] + 8).toBe(measured.cards[1].rect[1])
    expect(measured.cards[1].rect[1] + measured.cards[1].rect[3] + 8).toBe(measured.cards[2].rect[1])
    expect(measured.cards[2].rect[1] + measured.cards[2].rect[3] + 8).toBe(measured.aside[1] + measured.aside[3])
    expect(measured.aside[0] + measured.aside[2]).toBe(measured.main[0] + measured.main[2] - 0.5)
    // Tab strip: 28px pills with 2px margins, 363px wide strip, rows start 10px below it and are 42px high, 1px apart.
    const summaryTop = measured.cards[2].rect[1]
    expect(measured.tabs.map(tab => tab[1] - summaryTop)).toEqual([12.5, 12.5, 12.5, 12.5])
    expect(measured.tabs[0][0] - measured.cards[2].rect[0]).toBe(14.5)
    expect(measured.tabs.map(tab => tab[3])).toEqual([28, 28, 28, 28])
    expect(measured.rows).toHaveLength(2)
    expect(measured.rows[0][1] - summaryTop).toBe(50.5)
    expect(measured.rows[1][1] - measured.rows[0][1]).toBe(43)
    expect(measured.rows.map(row => [row[0] - measured.cards[2].rect[0], row[2], row[3]])).toEqual(measured.rows.map(() => [12.5, 363, 42]))
  })
}

test('defaults the saved view details open, drills into unassigned issues, and remembers the toggle', async ({ page, request, workspaceKey, issueList }) => {
  const response = await request.post('/api/views', {
    headers: { 'X-Workspace-Key': workspaceKey },
    data: { name: '[audit] Saved queue', resource: 'issues', scope: 'personal', view: 'all', filters: [], display: { grouping: 'status' } },
  })
  expect(response.status()).toBe(201)
  const view = await response.json() as SavedView
  await page.goto(`/${workspaceKey}/view/${view.slugId || view.id}`)
  await expect(page.locator('[data-content-view-subheader] [class*="viewCount"]')).toHaveCSS('font-size', '13px')
  await expect(page.locator('[data-content-view-subheader] [class*="viewCount"]')).toHaveCSS('font-weight', '500')
  const panel = page.getByRole('complementary', { name: 'View sidebar' })
  await expect(panel).toBeVisible()
  await expect(panel.getByText('Personal', { exact: true })).toBeVisible()
  await expect(panel.getByText('Owner', { exact: true })).toBeVisible()
  await expect(panel.getByText('Owner', { exact: true })).toHaveCSS('font-size', '12px')
  await expect(panel.getByText('Visibility', { exact: true })).toHaveCSS('color', 'lch(63.304 1.425 272)')
  await expect(panel.getByText('Personal', { exact: true })).toHaveCSS('color', 'lch(100 0 272)')
  await expect(panel.getByRole('switch', { name: 'Add to favorites', exact: true })).toHaveCSS('width', '28px')
  await expect(panel.getByRole('tablist', { name: 'View summary' })).toHaveCSS('gap', '2px')
  await expect(panel.getByRole('tab', { name: 'Labels', exact: true })).toHaveCSS('color', 'lch(63.304 1.425 272)')
  await expect(page.locator('[data-content-view-header] [data-header-row]')).toHaveCSS('padding-left', '8px')
  await expect(page.locator('[data-content-view-title] h2')).toHaveCSS('color', 'lch(90.451 1.2 272)')
  await expect(page.getByRole('button', { name: 'Close view details', exact: true })).toHaveCSS('background-color', 'lch(18.634 1.075 272)')
  await expect(panel.getByText(issueList.data.viewer.displayName, { exact: true }).first()).toBeVisible()
  expect((await panel.locator('[class*="identityCard"]').boundingBox())?.width).toBe(388)
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/desktop-saved-view.png`, animations: 'disabled' })
  await panel.getByRole('button', { name: 'No assignee 2', exact: true }).click()
  await expect(page.locator('[data-issue-row-id]')).toHaveCount(2)
  await page.getByRole('button', { name: 'Clear all filters', exact: true }).click()
  await expect(page.locator('[data-issue-row-id]')).toHaveCount(3)
  await page.getByRole('button', { name: 'Close view details', exact: true }).click()
  await expect(panel).toHaveCount(0)
  await page.reload()
  await expect(page.getByRole('button', { name: 'Open view details', exact: true })).toBeVisible()
  await expect(panel).toHaveCount(0)
  await page.getByRole('button', { name: 'Open view details', exact: true }).click()
  await page.reload()
  await expect(panel).toBeVisible()
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(panel).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Open view details', exact: true })).toBeVisible()
  await page.setViewportSize({ width: 1440, height: 900 })
  await expect(panel).toBeVisible()
})
