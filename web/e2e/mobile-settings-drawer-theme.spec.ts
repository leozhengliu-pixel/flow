import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, launchOptions: { args: ['--force-device-scale-factor=3'] } })

test('light settings drawer uses the theme search and selected backgrounds, including blurred typed search', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/settings/account/preferences`)
  await page.getByRole('button', { name: 'Settings navigation' }).click()
  await page.evaluate(() => { document.documentElement.dataset.theme = 'light' })
  const active = page.locator('.settings-sidebar nav [aria-current=page]')
  expect(await active.evaluate(element => getComputedStyle(element).backgroundColor)).toBe(await page.evaluate(() => {
    const probe = document.createElement('span'); probe.style.background = 'var(--set-nav-active-bg)'; document.body.append(probe)
    const color = getComputedStyle(probe).backgroundColor; probe.remove(); return color
  }))
  const search = page.locator('.settings-search input')
  await search.fill('Preferences')
  await page.locator('.settings-back').focus()
  expect(await search.evaluate(element => getComputedStyle(element).backgroundColor)).toBe(await page.evaluate(() => {
    const probe = document.createElement('span'); probe.style.background = 'var(--set-search-bg)'; document.body.append(probe)
    const color = getComputedStyle(probe).backgroundColor; probe.remove(); return color
  }))
  await expect(search).toHaveValue('Preferences')
})
