import { expect } from '@playwright/test'
import { test } from './issue-list-fixture'

test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })

test('uses desktop column gutters and the hidden status line height', async ({ page, request, workspaceKey, issueList }) => {
  const response = await request.get('/api/bootstrap', { headers: { 'X-Workspace-Key': workspaceKey } })
  expect(response.ok()).toBeTruthy()
  const { teams } = await response.json() as { teams: Array<{ key: string }> }
  await page.goto(`/${workspaceKey}/team/${teams[0].key}/all`)
  await page.getByRole('button', { name: 'Display options', exact: true }).click()
  await page.getByRole('tab', { name: 'Board', exact: true }).click()
  await page.getByRole('checkbox', { name: 'Show empty columns', exact: true }).check()
  await page.keyboard.press('Escape')

  const column = page.locator('[data-board-column]').first()
  await expect(column).toBeVisible()
  const title = page.getByText(issueList.issues[0].title, { exact: true })
  await expect(title).toHaveCSS('color', 'lch(100 0 272)')
  await expect(column).toHaveCSS('width', '356px')
  const header = column.locator('[data-board-column-header] > header')
  await expect(header).toHaveCSS('padding', '2px 12px 0px 18px')
  await expect(column.locator('[data-board-column-header]')).toHaveCSS('margin-left', '4px')
  await expect(header.getByRole('button', { name: 'Open menu', exact: true })).toHaveCSS('padding', '0px 2px')
  await header.getByRole('button', { name: 'Open menu', exact: true }).click()
  await page.getByRole('menuitem', { name: 'Hide column', exact: true }).click()
  const hidden = page.locator('[data-board-hidden-status]').first()
  await expect(hidden).toBeVisible()
  await expect(hidden.locator('span').first()).toHaveCSS('line-height', '18.2px')
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/desktop-board.png`, animations: 'disabled' })
  await hidden.click()
  await expect(page.locator('[data-board-hidden-status]')).toHaveCount(0)
})
