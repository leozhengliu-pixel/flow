import { expect, type Locator } from '@playwright/test'
import { writeFile } from 'node:fs/promises'
import { test } from './workspace-fixture'

async function expectContainedToolbar(toolbar: Locator) {
  const geometry = await toolbar.evaluate(element => {
    const pill = element.getBoundingClientRect()
    const panel = element.closest('.document-history-main')!.getBoundingClientRect()
    const restore = element.querySelector('.document-history-restore')!
    const range = document.createRange()
    range.selectNodeContents(restore)
    return {
      left: pill.left - panel.left, right: panel.right - pill.right,
      overflowing: [...element.querySelectorAll('button, label, span')].some(child => {
        const box = child.getBoundingClientRect()
        return box.width > 0 && (box.left < pill.left || box.right > pill.right)
      }),
      lines: range.getClientRects().length,
      contrast: (() => {
        const canvas = document.createElement('canvas')
        const context = canvas.getContext('2d')!
        const luminance = (color: string) => {
          context.fillStyle = color
          context.fillRect(0, 0, 1, 1)
          const [r, g, b] = [...context.getImageData(0, 0, 1, 1).data].slice(0, 3).map(value => {
            const channel = value / 255
            return channel <= .04045 ? channel / 12.92 : ((channel + .055) / 1.055) ** 2.4
          })
          return r * .2126 + g * .7152 + b * .0722
        }
        const background = luminance(getComputedStyle(element).backgroundColor)
        const foreground = luminance(getComputedStyle(element).color)
        return (Math.max(background, foreground) + .05) / (Math.min(background, foreground) + .05)
      })(),
    }
  })
  expect(geometry.left).toBeGreaterThanOrEqual(0)
  expect(geometry.right).toBeGreaterThanOrEqual(0)
  expect(geometry.overflowing).toBe(false)
  expect(geometry.lines).toBe(1)
  expect(geometry.contrast).toBeGreaterThanOrEqual(4.5)
}

for (const theme of ['light', 'dark'] as const) {
  for (const locale of ['en-US', 'zh-CN']) {
    for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }, { width: 1440, height: 900 }]) {
      test(`history toolbar fits ${viewport.width}px ${theme} ${locale}`, async ({ page, request, workspaceKey }) => {
        const headers = { 'X-Workspace-Key': workspaceKey }
        expect((await request.patch('/api/account/settings', { headers, data: { language: locale, interfaceTheme: theme } })).ok()).toBe(true)
        expect((await request.patch('/api/account/profile', { headers, data: { displayName: locale === 'zh-CN' ? '\u4e2d \u6587' : 'History reviewer', username: 'history-reviewer' } })).ok()).toBe(true)
        const title = locale === 'zh-CN' ? '\u6587\u6863\u5386\u53f2\u8bb0\u5f55\u548c\u7248\u672c\u6062\u590d\u7684\u5b8c\u6574\u8bf4\u660e' : 'Document history and version restoration'
        const response = await request.post('/api/documents', { headers, data: { title, content: 'Current paragraph.\n\nAnother current paragraph.' } })
        expect(response.status()).toBe(201)
        const document = await response.json() as { id: string; slugId: string; updatedAt: string }
        for (const [index, content] of ['Previous paragraph.\n\nAnother previous paragraph.', 'Original paragraph.'].entries()) {
          const imported = await request.post(`/api/workspace/documents/${document.id}/revisions/import`, {
            headers, data: { title, content, createdAt: new Date(Date.parse(document.updatedAt) - (index + 1) * 60_000).toISOString() },
          })
          expect(imported.status()).toBe(201)
        }
        await page.setViewportSize(viewport)
        await page.emulateMedia({ colorScheme: theme })
        await page.addInitScript(({ locale, theme }) => {
          localStorage.setItem('flow:locale', locale)
          localStorage.setItem('flow.theme', JSON.stringify({ interfaceTheme: theme }))
        }, { locale, theme })
        await page.goto(`/${workspaceKey}/document/${document.slugId}`)
        await page.locator('.document-edited').click()
        await page.locator('.document-history-action button').click()
        const dialog = page.locator('.document-history-dialog')
        await expect(dialog).toBeVisible()
        await dialog.evaluate(element => Promise.all(element.getAnimations({ subtree: true }).map(animation => animation.finished)))
        await page.evaluate(() => document.fonts.ready)
        const toolbar = dialog.getByRole('toolbar')
        const restore = dialog.locator('.document-history-restore')
        const prefix = `${viewport.width}-${theme}-${locale}`
        const capture = async (state: string) => {
          if (process.env.FLOW_HISTORY_SHOTS) await toolbar.screenshot({ path: `${process.env.FLOW_HISTORY_SHOTS}/${prefix}-${state}.png` })
        }
        if (process.env.FLOW_HISTORY_SHOTS) await page.screenshot({ path: `${process.env.FLOW_HISTORY_SHOTS}/${prefix}.png` })
        await capture('current')
        await expectContainedToolbar(toolbar)
        await expect(restore).toBeDisabled()
        const next = toolbar.locator('.document-history-navigator > button').last()
        await next.hover()
        await capture('next-hover')
        await next.focus()
        await page.keyboard.press('Shift+Tab')
        await page.keyboard.press('Tab')
        expect(await next.evaluate(element => element.matches(':focus-visible'))).toBe(true)
        await capture('next-focus')
        const counter = dialog.getByTestId('history-change-counter')
        const firstChange = (await counter.textContent())!
        await next.click()
        await expect(counter).not.toHaveText(firstChange)
        await toolbar.locator('.document-history-navigator > button').first().click()
        await expect(counter).toHaveText(firstChange)
        if (viewport.width < 721) {
          await dialog.locator('select').selectOption({ index: 1 })
        } else {
          await dialog.getByRole('option').nth(1).click()
        }
        await expect(restore).toBeEnabled()
        await restore.hover()
        await capture('hover')
        await restore.focus()
        await page.keyboard.press('Shift+Tab')
        await page.keyboard.press('Tab')
        await expect(restore).toBeFocused()
        expect(await restore.evaluate(element => element.matches(':focus-visible'))).toBe(true)
        await capture('focus')
        await expectContainedToolbar(toolbar)
        const toggle = toolbar.getByRole('checkbox')
        if (await toggle.isVisible()) {
          await toggle.click()
          await expect(toggle).not.toBeChecked()
          await capture('highlight-off')
          await expectContainedToolbar(toolbar)
          await toggle.click()
          await expect(toggle).toBeChecked()
        }
        if (viewport.width < 721) {
          await dialog.locator('select').selectOption({ index: 2 })
        } else {
          await dialog.getByRole('option').nth(2).click()
        }
        await expect(toolbar.getByRole('checkbox')).toHaveCount(0)
        await expectContainedToolbar(toolbar)
        await capture('restore-only')
        if (viewport.width < 721) {
          await expect(toolbar).toHaveCSS('height', '40px')
          const pillBox = (await toolbar.boundingBox())!
          const restoreBox = (await restore.boundingBox())!
          expect(restoreBox.y - pillBox.y).toBeCloseTo(6, 2)
        }
        if (process.env.FLOW_HISTORY_SHOTS) {
          const metrics = await restore.evaluate(element => {
            const pill = element.closest('.document-history-pill')!
            const properties = ['height', 'padding', 'fontSize', 'fontWeight', 'lineHeight', 'color', 'backgroundColor', 'border', 'borderRadius', 'boxShadow', 'whiteSpace'] as const
            const measure = (node: Element) => ({ rect: node.getBoundingClientRect().toJSON(), styles: Object.fromEntries(properties.map(key => [key, getComputedStyle(node)[key]])) })
            return { button: measure(element), pill: measure(pill) }
          })
          await writeFile(`${process.env.FLOW_HISTORY_SHOTS}/${prefix}-measurements.json`, JSON.stringify(metrics, null, 2))
        }
        const buttonBox = (await restore.boundingBox())!
        await page.mouse.move(buttonBox.x + buttonBox.width / 2, buttonBox.y + buttonBox.height / 2)
        await page.mouse.down()
        await capture('pressed')
        await page.mouse.up()
        await expect(dialog).not.toBeVisible()
        await expect(page.locator('.document-editor')).toContainText('Original paragraph.')
      })
    }
  }
}

for (const theme of ['light', 'dark'] as const) {
  for (const locale of ['en-US', 'zh-CN']) {
    test(`long change counter fits 375px ${theme} ${locale}`, async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      expect((await request.patch('/api/account/settings', { headers, data: { language: locale, interfaceTheme: theme } })).ok()).toBe(true)
      const body = (version: string) => Array.from({ length: 123 }, (_, index) => `${version} paragraph ${index + 1}.`).join('\n\n')
      const response = await request.post('/api/documents', { headers, data: { title: 'A', content: body('Current') } })
      expect(response.status()).toBe(201)
      const document = await response.json() as { id: string; slugId: string; updatedAt: string }
      const imported = await request.post(`/api/workspace/documents/${document.id}/revisions/import`, {
        headers, data: { title: 'A', content: body('Previous'), createdAt: new Date(Date.parse(document.updatedAt) - 60_000).toISOString() },
      })
      expect(imported.status()).toBe(201)
      await page.setViewportSize({ width: 375, height: 667 })
      await page.emulateMedia({ colorScheme: theme })
      await page.addInitScript(locale => localStorage.setItem('flow:locale', locale), locale)
      await page.goto(`/${workspaceKey}/document/${document.slugId}`)
      await page.locator('.document-edited').click()
      await page.locator('.document-history-action button').click()
      const toolbar = page.locator('.document-history-pill')
      await expect(page.getByTestId('history-change-counter')).toContainText('123')
      await toolbar.locator('.document-history-navigator > button').first().click()
      await expect(page.getByTestId('history-change-counter')).toContainText(locale === 'zh-CN' ? '123 \u5904\uff0c\u5171 123' : '123 of 123')
      await expectContainedToolbar(toolbar)
      if (process.env.FLOW_HISTORY_SHOTS) await toolbar.screenshot({ path: `${process.env.FLOW_HISTORY_SHOTS}/375-${theme}-${locale}-long-counter.png` })
    })
  }
}
