import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

const base = process.env.FLOW_MOBILE_BASE_URL ?? 'http://127.0.0.1:4173'

for (const width of [1440, 390, 375]) for (const theme of ['dark', 'light']) {
  test(`project calendar fits at ${width}px in ${theme}`, async ({ page, request, workspaceKey }, testInfo) => {
    test.skip((width < 768) !== (testInfo.project.name === 'mobile-chromium'), 'Run each viewport once')
    await page.setViewportSize({ width, height: width === 1440 ? 900 : width === 390 ? 844 : 667 })
    const response = await request.post(`${base}/api/projects`, { headers: { 'X-Workspace-Key': workspaceKey }, data: { name: 'Calendar bounds' } })
    expect(response.status()).toBe(201)
    const project = await response.json() as { slugId: string }
    await page.goto(`${base}/${workspaceKey}/project/${project.slugId}/overview`)
    const targets = page.getByRole('button', { name: 'Change project target date', exact: true })
    await expect(targets.first()).toBeVisible()
    await page.evaluate(theme => { document.documentElement.dataset.theme = theme }, theme)
    for (const target of await targets.all()) {
      if (await target.evaluate(element => {
        const box = element.getBoundingClientRect()
        return element.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2))
      })) { await target.click(); break }
    }
    const panel = page.locator('[data-radix-popper-content-wrapper] > div').filter({ has: page.getByRole('tablist', { name: 'Date precision' }) })
    await expect(panel).toBeVisible()
    await expect(panel).toHaveCSS('height', '430px')
    await expect.poll(() => panel.evaluate(element => element.scrollHeight <= element.clientHeight)).toBe(true)
    const lastDay = panel.getByRole('gridcell').last()
    await expect.poll(() => lastDay.evaluate(element => {
      const box = element.getBoundingClientRect()
      return element.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2))
    })).toBe(true)
  })
}
