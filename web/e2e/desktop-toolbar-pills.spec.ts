import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark', launchOptions: { args: ['--force-device-scale-factor=2'] } })

test('paints toolbar icon pills separately from text tabs', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/my-issues/subscribed`)
  await page.locator('[data-my-issues-surface]').waitFor()
  const filter = page.getByRole('button', { name: 'Add filter', exact: true })
  const toolbar = page.locator('[data-toolbar-buttons-navigation]')
  const capture = async (state: string) => {
    if (process.env.FLOW_SWEEP_SHOTS) await toolbar.screenshot({ path: `${process.env.FLOW_SWEEP_SHOTS}/toolbar-${state}.png` })
  }
  await expect(filter).toHaveCSS('background-color', 'lch(10.149 0.689 272)')
  await capture('rest')
  await filter.hover()
  await expect(filter).toHaveCSS('background-color', 'lch(14.777 1.075 272)')
  await expect(filter.locator('svg')).toHaveCSS('fill', 'lch(100 0 272)')
  await capture('hover')
  await page.mouse.move(0, 0)
  await filter.focus()
  await page.keyboard.press('Shift+Tab')
  await page.keyboard.press('Tab')
  await expect(filter).toBeFocused()
  await expect(filter).toHaveCSS('outline-width', '1px')
  await capture('focus')
  await filter.click()
  await expect(filter).toHaveAttribute('data-state', 'open')
  await expect(filter).toHaveCSS('background-color', 'lch(18.634 1.075 272)')
  await capture('open')
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: 'Open details', exact: true }).click()
  const details = page.getByRole('button', { name: 'Close details', exact: true })
  await expect(details).toHaveAttribute('aria-pressed', 'true')
  await expect(details).toHaveCSS('background-color', 'lch(18.634 1.075 272)')
  await capture('selected')
})
