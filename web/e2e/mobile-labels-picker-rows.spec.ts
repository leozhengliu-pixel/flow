import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import { bootstrap, preferences } from './review-findings-fixture'

// Match the phone gate's native scale so Chromium lays out half-pixel borders.
const userAgent = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1'
test.use({ deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent, launchOptions: { args: ['--force-device-scale-factor=2'] } })

for (const width of [375, 390]) for (const theme of ['Dark', 'Light'] as const) for (const locale of ['en-US', 'zh-CN']) {
  test(`labels use the shared phone row geometry ${width} ${theme} ${locale}`, async ({ page, request, workspaceKey }) => {
    await page.setViewportSize({ width, height: width === 375 ? 667 : 844 })
    await preferences(request, workspaceKey, theme, locale)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const data = await bootstrap(request, workspaceKey)
    if (!data.labels.some(label => label.name === 'Bug')) {
      const labelResponse = await request.post('/api/labels', { headers, data: { name: 'Bug', color: '#eb5757' } })
      expect(labelResponse.status()).toBe(201)
    }
    const issueResponse = await request.post('/api/issues', { headers, data: { title: '中文 Label picker', teamId: data.teams[0].id, stateId: data.states.find(state => state.type === 'unstarted')!.id } })
    expect(issueResponse.status()).toBe(201)
    const issue = await issueResponse.json()
    await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
    const trigger = page.locator('.labels-picker .label-project-trigger:visible').first()
    await trigger.click()
    const menu = page.locator('.phone-menu-surface.property-command-labels')
    const option = menu.getByRole('option', { name: 'Bug', exact: true })
    await expect(option).toBeVisible()
    await menu.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
    expect((await menu.boundingBox())!.width).toBe(width - 32)
    expect((await menu.boundingBox())!.x).toBe(16)
    await expect(menu.locator('[role=option][aria-selected=true]')).toHaveCount(0)
    const label = option.locator('.property-command-label')
    await expect(label).toHaveCSS('font-size', '13px')
    await expect(label).toHaveCSS('font-weight', '450')
    expect((await option.boundingBox())!.width).toBe(width - 44)
    expect((await label.boundingBox())!.x).toBe(86.5)
    expect((await option.locator('.property-command-checkbox').boundingBox())!.x).toBe(35.5)
    await option.hover()
    await expect(option.locator('.property-command-checkbox')).toHaveCSS('opacity', '1')
    await menu.getByRole('textbox').focus()
    for (let index = 0; index < await menu.getByRole('option').count(); index++) {
      await menu.getByRole('textbox').press('ArrowDown')
      if (await option.getAttribute('aria-selected') === 'true') break
    }
    await expect(option).toHaveAttribute('aria-selected', 'true')
    await expect(option.locator('.property-command-checkbox')).toHaveCSS('opacity', '1')
    await option.click()
    await expect(option).toHaveAttribute('aria-checked', 'true')
    if (process.env.FLOW_CI_SHOTS && (width === 390 && theme === 'Dark' && locale === 'en-US' || width === 375 && theme === 'Light' && locale === 'zh-CN')) await menu.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/labels-phone-${width}-${theme}-${locale}.png` })
    await page.keyboard.press('Escape')
    await expect(trigger).toBeFocused()
  })
}

for (const width of [375, 390]) {
  test(`composer labels preserve the panel and content geometry ${width}`, async ({ page, request, workspaceKey }) => {
    await page.setViewportSize({ width, height: width === 375 ? 667 : 844 })
    await preferences(request, workspaceKey, 'Dark')
    const data = await bootstrap(request, workspaceKey)
    for (const name of ['Bug', 'Feature', 'Improvement']) {
      if (!data.labels.some(label => label.name === name)) {
        expect((await request.post('/api/labels', { headers: { 'X-Workspace-Key': workspaceKey }, data: { name, color: '#eb5757' } })).status()).toBe(201)
      }
    }
    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await page.getByRole('button', { name: 'New issue', exact: true }).click()
    await page.locator('[role=dialog] [data-property=labels]').click()
    const menu = page.locator('.phone-menu-surface.property-command-labels')
    await expect(menu.getByRole('option')).toHaveCount(3)
    await menu.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
    const panel = (await menu.boundingBox())!
    expect(panel.x).toBe(16)
    expect(panel.width).toBe(width - 32)
    expect(panel.height).toBe(260)
    const search = (await menu.locator('.property-command-search').boundingBox())!
    expect(search.x).toBe(22.5)
    expect(search.width).toBe(width - 44)
    const bug = menu.getByRole('option', { name: 'Bug', exact: true })
    expect((await bug.boundingBox())!.width).toBe(width - 44)
    expect((await bug.locator('.property-command-checkbox').boundingBox())!.x).toBe(35.5)
    expect((await bug.locator('.property-command-label').boundingBox())!.x).toBe(86.5)
  })

  test(`labels sizing leaves notification actions at their own width ${width}`, async ({ page, request, workspaceKey }) => {
    await page.setViewportSize({ width, height: 844 })
    await preferences(request, workspaceKey, 'Dark')
    await page.goto(`/${workspaceKey}/inbox`)
    await page.getByRole('button', { name: 'Notification actions', exact: true }).click()
    const menu = page.locator('.inbox-actions-phone-menu')
    await expect(menu).toBeVisible()
    await menu.evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)))
    for (const action of ['delete-all', 'delete-all-read', 'delete-all-completed', 'settings']) {
      const box = (await menu.getByTestId(`inbox-actions-${action}`).boundingBox())!
      expect(box.x).toBe(22.5)
      expect(box.width).toBe(width - 44)
    }
    const divider = (await menu.getByTestId('inbox-actions-divider').boundingBox())!
    expect(divider.x).toBe(16.5)
    expect(divider.width).toBe(width - 32)
  })
}
