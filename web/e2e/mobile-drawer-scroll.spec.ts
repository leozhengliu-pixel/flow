import { expect } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { test } from './workspace-fixture'

for (const width of [1440, 390, 375]) {
  for (const theme of ['dark', 'light']) {
    for (const locale of ['en-US', 'zh-CN']) {
      test(`drawer header scroll boundary: ${width} ${theme} ${locale}`, async ({ page, request, workspaceKey }) => {
        const phone = width < 768
        const crops: { label: string; image: string }[] = []
        const capture = async (label: string) => {
          if (!process.env.FLOW_DRAWER_SHOTS) return
          const image = await page.screenshot({ clip: { x: 0, y: 0, width: phone ? width : 350, height: 220 }, animations: 'disabled' })
          crops.push({ label, image: image.toString('base64') })
        }
        await page.setViewportSize({ width, height: phone ? width === 390 ? 844 : 667 : 900 })
        await page.addInitScript(({ theme, locale }) => {
          localStorage.setItem('flow.theme', JSON.stringify({ interfaceTheme: theme }))
          localStorage.setItem('flow:locale', locale)
        }, { theme, locale })
        const headers = { 'X-Workspace-Key': workspaceKey }
        const name = locale === 'zh-CN' ? '\u5f20 \u4f1f' : theme === 'dark' ? 'A' : 'Customer reporting platform migration'
        expect((await request.patch(`/api/workspaces/${workspaceKey}`, { headers, data: { name } })).ok()).toBe(true)
        expect((await request.patch('/api/account/settings', { headers, data: { language: locale, interfaceTheme: theme } })).ok()).toBe(true)
        for (const [index, name] of ['A', 'Customer reporting platform migration', '\u5f20\u4f1f', 'Release readiness'].entries()) {
          expect((await request.post(`/api/workspaces/${workspaceKey}/teams`, { headers, data: { name, key: `D${index}` } })).status()).toBe(201)
        }
        await page.goto(`/${workspaceKey}/my-issues/assigned`)
        await expect(page.locator('.flow-startup')).toHaveCount(0)
        await page.keyboard.press('Tab')
        const skip = page.locator('.skip-to-content')
        await expect(skip).toBeFocused()
        await expect(skip).toHaveCSS('position', 'fixed')
        await capture('Skip link / keyboard focus')
        await page.keyboard.press('Enter')
        await expect(page.locator('main')).toBeFocused()
        const drawer = page.locator('[data-sidebar-surface]')
        const settled = async () => {
          await expect.poll(async () => Math.abs((await drawer.boundingBox())!.x)).toBeLessThan(0.5)
        }
        if (phone) await page.locator('[data-sidebar-trigger]').click()
        await settled()
        const nav = page.locator('.sidebar-nav')
        const scrollTop = () => nav.evaluate(element => element.scrollTop)
        await expect.poll(scrollTop).toBe(0)
        await page.mouse.move(width - 1, 0)
        await capture('Drawer / open, selected navigation')
        for (const team of await page.locator('.sidebar-team .team-title').all()) {
          if (await team.getAttribute('aria-expanded') === 'false') await team.click()
        }
        await page.locator('.sidebar-team .team-title').last().focus()
        await page.keyboard.press('Tab')
        await nav.evaluate(element => { element.scrollTop = element.scrollHeight })
        await expect.poll(scrollTop).toBeGreaterThan(0)
        const previousScroll = await scrollTop()
        await page.locator('.workspace-menu-trigger').focus()
        await expect.poll(scrollTop).toBe(phone ? 0 : previousScroll)
        await expect(page.locator('.workspace-menu-trigger')).toHaveCSS('outline-width', '1px')
        await capture('Header / focus-visible after returning from lower navigation')
        await page.locator('.sidebar-top-action').first().hover()
        await capture('Header / search hover')
        if (phone) {
          await nav.evaluate(element => { element.scrollTop = element.scrollHeight })
          await expect.poll(scrollTop).toBeGreaterThan(0)
          await page.locator('.sidebar-scrim').click({ position: { x: width - 1, y: 300 } })
          await expect(drawer).toHaveAttribute('aria-hidden', 'true')
          await page.locator('[data-sidebar-trigger]').click()
          await settled()
          await expect.poll(scrollTop).toBe(0)
          await capture('Drawer / reopened at top')
        }
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
        if (process.env.FLOW_DRAWER_SHOTS) {
          const sheet = await page.context().newPage()
          await sheet.setViewportSize({ width: 1200, height: 900 })
          await sheet.setContent(`<html><style>body{margin:20px;font:14px system-ui}main{display:grid;grid-template-columns:repeat(3,1fr);gap:16px}figure{margin:0}img{max-width:100%;display:block}figcaption{margin-bottom:8px}</style><h1>${width}px / ${theme} / ${locale}</h1><main>${crops.map(crop => `<figure><figcaption>${crop.label}</figcaption><img src="data:image/png;base64,${crop.image}"></figure>`).join('')}</main></html>`)
          await mkdir(process.env.FLOW_DRAWER_SHOTS, { recursive: true })
          await sheet.screenshot({ path: `${process.env.FLOW_DRAWER_SHOTS}/drawer-${width}-${theme}-${locale}.png`, fullPage: true })
          await sheet.close()
        }
      })
    }
  }
}
