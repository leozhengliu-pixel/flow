import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.skip(({ isMobile }) => isMobile, 'desktop dialog geometry')

test.use({ viewport: { width: 1470, height: 688 }, deviceScaleFactor: 2, colorScheme: 'light' })

// The reference app's "Custom date or timeframe…" dialog under Dates: day / month / quarter / half-year / year,
// a cell applies at once as "within", and the chip's value reopens the dialog on the applied period.
test('applies a month from the custom date dialog and reopens it from the chip', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as { teams: Array<{ id: string; key: string }> }
  const team = data.teams[0]
  await request.post('/api/issues', { headers, data: { title: 'Custom date sample', teamId: team.id, stateId: 'state_todo' } })
  await page.goto(`/${workspaceKey}/team/${team.key}/all`)
  await page.getByRole('button', { name: 'Add filter', exact: true }).click()
  await page.getByRole('option', { name: /^Dates/ }).hover()
  await page.getByRole('option', { name: /^Created date/ }).hover()
  await page.getByRole('option', { name: 'Custom date or timeframe…', exact: true }).click()

  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('tab', { name: 'Quarter' })).toHaveAttribute('aria-selected', 'true')
  const panel = dialog.locator('form').locator('..')
  // Hairline borders render as 0.5px or 1px depending on the browser build, so compare the sections, not the outer height.
  await expect.poll(async () => { const box = await panel.boundingBox(); return box && [box.x, box.width] }).toEqual([454, 562])
  await expect.poll(() => dialog.locator('form').evaluate(form => [...form.children].map(section => Math.round(section.getBoundingClientRect().height)))).toEqual([167, 298, 89])
  await expect(dialog.getByRole('textbox')).toHaveAttribute('placeholder', 'Try: May 2027, Q4, 05/20/2027')

  const now = new Date()
  const month = now.toLocaleString('en-US', { month: 'short' })
  await dialog.getByRole('tab', { name: 'Month' }).click()
  await dialog.locator(`[data-year="${now.getFullYear()}"]`).getByRole('button', { name: month, exact: true }).click()
  await expect(dialog).toBeHidden()

  const values = page.getByRole('button', { name: 'Created date values', exact: true })
  await expect(page.getByRole('button', { name: 'Created date operator', exact: true })).toHaveText('within')
  await expect(values).toHaveText(`${month} ${now.getFullYear()}`)
  await expect(page.getByText('Custom date sample', { exact: true })).toBeVisible()

  await values.click()
  await expect(dialog.getByRole('tab', { name: 'Month' })).toHaveAttribute('aria-selected', 'true')
  await expect(dialog.getByRole('textbox')).toHaveValue(`${month} ${now.getFullYear()}`)
  await expect(dialog.locator(`[data-year="${now.getFullYear()}"]`).getByRole('button', { name: month, exact: true })).toHaveAttribute('data-selected')
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(dialog).toBeHidden()
})
