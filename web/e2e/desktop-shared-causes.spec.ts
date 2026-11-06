import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark', launchOptions: { args: ['--force-device-scale-factor=2'] } })
test.skip(({ isMobile }) => isMobile, 'Desktop shared chrome')

test('shared sidebar and agent header retain their layout and declared insets', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/agent`)
  const switcher = page.getByRole('button', { name: 'Switch agent chat', exact: true })
  await expect(switcher).toBeVisible()
  const sidebar = page.locator('.sidebar')
  expect(await sidebar.evaluate(node => {
    const probe = document.createElement('span')
    probe.style.color = 'var(--sidebar-nav-fg)'
    node.append(probe)
    const matches = getComputedStyle(node.querySelector('.section-label')!).color === getComputedStyle(probe).color
    probe.remove()
    return matches
  })).toBe(true)
  await expect(sidebar.locator('.sidebar-more-trigger')).toHaveCSS('padding', '0px 9px 0px 8px')
  await expect(page.locator('.bottom-agent > button').last()).toHaveCSS('padding', '0px 2px')
  await expect(page.locator('main > header')).toHaveCSS('padding', '0px')
  await expect(page.locator('main > header')).toHaveCSS('gap', '12px')
  const header = await page.locator('main > header').boundingBox()
  const title = await switcher.boundingBox()
  expect(title!.x - header!.x).toBe(10)
  await switcher.click()
  await expect(switcher).toHaveAttribute('aria-expanded', 'true')
  await page.keyboard.press('Escape')
  await expect(switcher).toHaveAttribute('aria-expanded', 'false')
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/shared-agent.png`, animations: 'disabled' })

  await page.goto(`/${workspaceKey}/loops`)
  await expect(page.locator('.loops-topbar')).toHaveCSS('padding', '0px')
  await expect(page.locator('.loops-topbar')).toHaveCSS('gap', '12px')
  const loopHeader = await page.locator('.loops-topbar').boundingBox()
  const loopTitle = await page.locator('.loops-title').boundingBox()
  expect(loopTitle!.x - loopHeader!.x).toBe(18)
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/shared-loops.png`, animations: 'disabled' })
  await page.evaluate(() => localStorage.setItem('flow.sidebar.collapsed', 'true'))
  await page.reload()
  const reveal = page.locator('.loops-topbar [data-sidebar-trigger]')
  await expect(reveal).toBeVisible()
  const triggerBox = await reveal.boundingBox()
  const collapsedTitle = await page.locator('.loops-title').boundingBox()
  expect(collapsedTitle!.x - triggerBox!.x - triggerBox!.width).toBe(8)
})

test('phone drawer and agent header keep their existing shared metrics', async ({ page, workspaceKey }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(`/${workspaceKey}/agent`)
  const switcher = page.getByRole('button', { name: 'Switch agent chat', exact: true })
  await expect(switcher).toHaveCSS('height', '32px')
  await page.locator('main [data-sidebar-trigger]').click()
  await expect(page.locator('.sidebar')).toBeVisible()
  await expect(page.locator('.sidebar .sidebar-more-trigger')).toHaveCSS('padding', '0px')
  await expect.poll(async () => (await page.locator('.sidebar').boundingBox())!.x).toBe(0)
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/shared-phone-drawer.png`, animations: 'disabled' })
})

test('desktop settings reuse quiet help and label-table controls', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/settings/api`)
  await expect(page.locator('.settings-desktop-docs').first()).toHaveCSS('font-size', '13px')
  await expect(page.locator('.settings-desktop-docs').first()).toHaveCSS('font-weight', '450')
  const help = page.locator('.settings-sidebar-footer .sidebar-help')
  await expect(help).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
  await expect(help).toHaveCSS('border-top-color', 'rgba(0, 0, 0, 0)')
  await expect(help).toHaveCSS('padding', '0px 4px')
  await expect(help).toHaveCSS('border-radius', '8px')
  const helpBox = (await help.boundingBox())!
  expect([helpBox.x, helpBox.width, helpBox.height]).toEqual([15, 25, 24])
  await page.goto(`/${workspaceKey}/settings/issue-labels`)
  await expect(page.getByRole('button', { name: 'Order by Name, sorted ascending', exact: true })).toHaveCSS('gap', '2px')
  await expect(page.getByRole('button', { name: 'Add filter', exact: true })).toHaveCSS('padding', '0px 2px')
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/shared-settings.png`, animations: 'disabled' })
})
