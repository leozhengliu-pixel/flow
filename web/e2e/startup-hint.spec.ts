import { expect } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { test } from './workspace-fixture'

for (const width of [1440, 390, 375]) {
  for (const theme of ['dark', 'light']) {
    for (const locale of ['en-US', 'zh-CN']) {
      test(`startup hint clock: ${width} ${theme} ${locale}`, async ({ page, workspaceKey }) => {
        await page.setViewportSize({ width, height: width === 1440 ? 900 : width === 390 ? 844 : 667 })
        await page.addInitScript(({ theme, locale }) => {
          localStorage.setItem('flow.theme', JSON.stringify({ interfaceTheme: theme }))
          localStorage.setItem('flow:locale', locale)
        }, { theme, locale })
        let release!: () => void
        let arrived!: () => void
        const pending = new Promise<void>(resolve => { release = resolve })
        const reached = new Promise<void>(resolve => { arrived = resolve })
        await page.route('**/api/auth/session', async route => {
          arrived()
          await pending
          await route.continue()
        })
        try {
          await page.goto(`/${workspaceKey}/my-issues/assigned`, { waitUntil: 'domcontentloaded' })
          await reached
          const hint = page.locator('.flow-startup__text')
          await expect(hint).toHaveCSS('opacity', '0')
          const clock = await hint.evaluate(element => {
            const style = getComputedStyle(element)
            const ready = (performance.getEntriesByType('navigation')[0] as PerformanceNavigationTiming).domContentLoadedEventStart
            return { duration: parseFloat(style.animationDuration) * 1000, easing: style.animationTimingFunction, delay: parseFloat(style.animationDelay) * 1000, ready }
          })
          expect(clock.duration).toBe(800)
          expect(clock.easing).toBe('ease-out')
          expect(clock.delay).toBeGreaterThanOrEqual(0)
          await expect(hint).toHaveCSS('opacity', '1', { timeout: 12_000 })
          if (process.env.FLOW_FOCUS_SHOTS) {
            await mkdir(process.env.FLOW_FOCUS_SHOTS, { recursive: true })
            await page.screenshot({ path: `${process.env.FLOW_FOCUS_SHOTS}/loading-${width}-${theme}-${locale}.png` })
          }
          release()
          await expect(page.locator('.flow-startup')).toHaveCount(0, { timeout: 30_000 })
        } finally {
          release()
        }
      })
    }
  }
}

test('keeps the hint clock paused until the entry finishes', async ({ page, workspaceKey }) => {
  let release!: () => void
  const pending = new Promise<void>(resolve => { release = resolve })
  await page.route('**/src/main.tsx', async route => {
    await pending
    await route.continue()
  })
  try {
    await page.goto(`/${workspaceKey}/my-issues/assigned`, { waitUntil: 'commit' })
    const hint = page.locator('.flow-startup__text')
    await expect(hint).toHaveCSS('animation-play-state', 'paused')
    await expect(hint).toHaveCSS('opacity', '0')
  } finally {
    release()
  }
})
