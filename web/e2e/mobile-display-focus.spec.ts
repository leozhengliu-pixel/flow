import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, launchOptions: { args: ['--force-device-scale-factor=3'] } })

test('shared Display options keeps a visible keyboard focus ring on phones', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/my-issues/assigned`)
  const trigger = page.getByRole('button', { name: 'Display options', exact: true })
  await expect(trigger).toBeVisible()
  await page.keyboard.press('Tab')
  await trigger.focus()
  expect(await trigger.evaluate(element => {
    const style = getComputedStyle(element)
    return element.matches(':focus-visible') && style.outlineStyle === 'solid' && style.outlineWidth === '1px' && style.outlineOffset === '0px'
  })).toBe(true)
  await trigger.press('Enter')
  await expect(page.getByRole('dialog', { name: 'Display options' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(trigger).toBeFocused()
})
