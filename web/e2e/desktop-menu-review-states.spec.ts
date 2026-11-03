import { expect, type Locator } from '@playwright/test'
import { writeFileSync } from 'node:fs'
import { test } from './workspace-fixture'

test.use({ deviceScaleFactor: 2, launchOptions: { args: ['--force-device-scale-factor=2'] } })
test.afterEach(async ({ request, workspaceKey }) => {
  await request.patch('/api/account/settings', { headers: { 'X-Workspace-Key': workspaceKey }, data: { interfaceTheme: 'System preference', language: 'en-US' } })
})

async function capture(locator: Locator, name: string) {
  if (!process.env.FLOW_REVIEW_SHOTS) return
  await locator.screenshot({ path: `${process.env.FLOW_REVIEW_SHOTS}/${name}.png`, animations: 'disabled' })
  writeFileSync(`${process.env.FLOW_REVIEW_SHOTS}/${name}.json`, JSON.stringify(await locator.evaluate(element => ({
    bounds: element.getBoundingClientRect().toJSON(),
    leaves: [...element.querySelectorAll<HTMLElement>('span,input,svg')].filter(node => node.children.length === 0).map(node => ({ text: node.textContent?.slice(0,80), className: node.getAttribute('class'), bounds: node.getBoundingClientRect().toJSON(), color: getComputedStyle(node).color, fill: getComputedStyle(node).fill })),
  })), null, 2))
}

for (const width of [1440, 375, 390]) for (const theme of ['dark', 'light'] as const) for (const locale of ['en-US', 'zh-CN']) {
  test(`shared menu states ${width} ${theme} ${locale}`, async ({ page, request, workspaceKey }) => {
    if (process.env.FLOW_REVIEW_SHOTS) test.setTimeout(90_000)
    const phone = width < 768, zh = locale === 'zh-CN', key = `${width}-${theme}-${locale}`
    await page.setViewportSize({ width, height: width === 1440 ? 900 : width === 375 ? 667 : 844 })
    await page.emulateMedia({ colorScheme: theme })
    await page.addInitScript(({ theme, locale }) => {
      localStorage.setItem('flow:locale', locale)
      localStorage.setItem('flow.theme', JSON.stringify({ interfaceTheme: theme === 'dark' ? 'Dark' : 'Light' }))
    }, { theme, locale })
    const headers = { 'X-Workspace-Key': workspaceKey }
    expect((await request.patch('/api/account/settings', { headers, data: { interfaceTheme: theme === 'dark' ? 'Dark' : 'Light', language: locale } })).ok()).toBe(true)
    const boot = await (await request.get('/api/bootstrap', { headers })).json()
    await page.route('**/api/bootstrap*', async route => {
      const response = await route.fetch(), data = await response.json()
      data.userSettings[data.viewer.id].language = locale
      data.userSettings[data.viewer.id].interfaceTheme = theme === 'dark' ? 'Dark' : 'Light'
      data.workspace.name = '中文 WW Workspace with a long realistic name'
      data.agentSkills = [
        { id: 'short-skill', name: 'W', instructions: 'x' },
        { id: 'long-skill', name: '整理跨团队周报并生成摘要与待办事项清单供评审使用', instructions: 'x' },
        { id: 'latin-skill', name: 'Write release notes for the quarterly platform reliability update', instructions: 'x' },
      ].map(skill => ({ ...skill, userId: data.viewer.id, createdAt: '2026-11-01T00:00:00Z', updatedAt: '2026-11-01T00:00:00Z' }))
      await route.fulfill({ response, json: data })
    })
    await page.goto(`/${workspaceKey}/agent`)
    await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
    await page.evaluate(() => document.fonts.ready)
    if (phone) await page.locator('[data-sidebar-trigger]').first().click()
    const trigger = page.locator('.workspace-menu-trigger')
    await capture(page.locator('.workspace-row'), `workspace-rest-${key}`)
    await trigger.hover()
    await page.keyboard.press('Tab')
    await trigger.focus()
    expect(await trigger.evaluate(element => element.matches(':focus-visible'))).toBe(true)
    await capture(page.locator('.workspace-row'), `workspace-focus-${key}`)
    await trigger.click()
    const workspaceMenu = page.locator(phone ? '.workspace-phone-menu' : '.workspace-primary-menu')
    await expect(workspaceMenu).toBeVisible()
    await capture(workspaceMenu, `workspace-open-${key}`)
    await page.keyboard.press('Escape')
    const teamTrigger = page.getByRole('button', { name: zh ? '团队菜单' : 'Team menu', exact: true }).first()
    await teamTrigger.click()
    const teamMenu = page.locator('.sidebar-team-menu')
    const settings = teamMenu.getByRole('menuitem', { name: zh ? '团队设置' : 'Team settings' })
    await settings.hover()
    const strong = theme === 'dark' ? 'lch(100 0 272)' : 'lch(10 0 282)'
    await expect(settings).toHaveCSS('color', strong)
    await expect(settings.locator('svg').first()).toHaveCSS('fill', 'none')
    await capture(teamMenu, `team-hover-${key}`)
    await page.keyboard.press('Home')
    await expect(teamMenu.getByRole('menuitem').first()).toBeFocused()
    await page.keyboard.press('ArrowDown')
    await expect(settings).toBeFocused()
    expect(await settings.evaluate(element => element.matches(':focus-visible'))).toBe(true)
    await capture(teamMenu, `team-focus-${key}`)
    const subscribe = teamMenu.getByRole('menuitem', { name: zh ? '订阅' : 'Subscribe', exact: true })
    if (phone) await subscribe.click(); else await subscribe.hover()
    const subscriptions = page.locator('.sidebar-team-subscribe-menu')
    await expect(subscriptions).toBeVisible()
    const heading = subscriptions.locator('[data-menu-heading]').first()
    await expect(heading).toHaveCSS('color', theme === 'dark' ? 'lch(64.714 1.425 272)' : 'lch(40 1 282)')
    const checkbox = subscriptions.getByRole('menuitemcheckbox').first()
    await checkbox.hover()
    await page.keyboard.press('Home')
    await expect(checkbox).toBeFocused()
    expect(await checkbox.evaluate(element => element.matches(':focus-visible'))).toBe(true)
    await capture(subscriptions, `subscribe-focus-${key}`)
    if (phone) {
      await expect(subscriptions.getByRole('textbox')).toBeVisible()
      const box = (await subscriptions.boundingBox())!
      expect(box.height).toBe(302)
      expect(await heading.evaluate(element => { const range = document.createRange(); range.selectNodeContents(element); return range.getBoundingClientRect().x })).toBe(42.5)
      expect((await checkbox.locator(':scope > span').last().boundingBox())!.x).toBe(62.5)
    }
    let releaseSave!: () => void
    const saveHeld = new Promise<void>(resolve => { releaseSave = resolve })
    await page.route('**/api/subscriptions', async route => { await saveHeld; await route.continue() })
    try {
      await checkbox.click()
      await expect(checkbox).toHaveAttribute('data-disabled')
      await capture(subscriptions, `subscribe-disabled-${key}`)
    } finally { releaseSave() }
    await expect(checkbox).not.toHaveAttribute('data-disabled')
    await expect(checkbox).toHaveAttribute('aria-checked', 'true')
    await capture(subscriptions, `subscribe-selected-${key}`)
    await page.keyboard.press('Escape')
    await page.keyboard.press('Escape')
    if (phone) await page.getByRole('button', { name: /Close sidebar|关闭侧边栏/ }).click({ position: { x: width - 10, y: 10 } })
    await page.getByRole('button', { name: zh ? '技能' : 'Skills', exact: true }).first().click()
    const skills = page.locator('[role=listbox]').filter({ has: page.getByRole('option', { name: 'W', exact: true }) })
    await expect(skills).toBeVisible()
    const longSkill = skills.getByRole('option', { name: /整理跨团队/ })
    await longSkill.hover()
    await expect(longSkill).toHaveCSS('color', strong)
    expect(await longSkill.locator('span').evaluate(element => element.getBoundingClientRect().width <= element.parentElement!.getBoundingClientRect().width)).toBe(true)
    await capture(skills, `skills-hover-${key}`)
    await page.keyboard.press('ArrowDown')
    await longSkill.focus()
    expect(await longSkill.evaluate(element => element.matches(':focus-visible'))).toBe(true)
    await capture(skills, `skills-focus-${key}`)
    await longSkill.click()
    await expect(longSkill.locator('svg')).toHaveCount(2)
    await capture(skills, `skills-selected-${key}`)
    await page.keyboard.press('Escape')
    const issueResponse = await request.post('/api/issues', { headers, data: { title: 'Review labels', teamId: boot.teams[0].id, stateId: 'state_todo', assigneeId: boot.viewer.id } })
    expect(issueResponse.status()).toBe(201)
    const issue = await issueResponse.json()
    await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
    await page.getByRole('button', { name: phone ? /Change labels|更改标签/ : /Add label|添加标签/ }).first().click()
    const labels = page.locator(phone ? '.phone-menu-surface.property-command-labels' : '.issue-labels-menu')
    await expect(labels).toBeVisible()
    const option = labels.getByRole('option').first(), mark = option.locator('.property-command-checkbox')
    await option.hover()
    await expect(mark).toBeVisible()
    await expect(mark).toHaveCSS('opacity', '1')
    expect((await mark.boundingBox())!.width).toBe(14)
    if (phone) {
      expect((await option.locator('.property-command-label').boundingBox())!.x).toBe(86.5)
      expect((await option.locator('.option-dot').boundingBox())!.width).toBe(9)
      expect((await mark.boundingBox())!.x).toBe(35.5)
    }
    if (!phone) await expect(mark).toHaveCSS('border-top-width', '1px')
    await capture(labels, `labels-hover-${key}`)
    await option.click()
    await expect(option).toHaveAttribute('aria-checked', 'true')
    await capture(labels, `labels-selected-${key}`)
    await page.keyboard.press('Escape')
    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    const issueRow = page.locator(`[data-issue-row-id="${issue.id}"]`)
    await expect(issueRow).toBeVisible()
    await issueRow.click({ button: 'right' })
    const context = page.locator('[role=menu]').last()
    await expect(context).toBeVisible()
    if (phone) {
      const filter = context.getByRole('textbox', { name: zh ? '筛选…' : 'Filter…' })
      await expect(filter).toBeVisible()
      expect(await context.locator('.flow-submenu-chevron:visible').count()).toBe(0)
      await filter.fill(zh ? '优先级' : 'Priority')
      await expect(context.getByRole('separator')).toHaveCount(0)
      await capture(context, `context-filtered-${key}`)
      await filter.fill('zzzz-no-match')
      await expect(context.getByRole('status')).toBeVisible()
      await capture(context, `context-empty-${key}`)
      await filter.clear()
      expect(await context.getByRole('menuitem').count()).toBeGreaterThan(1)
    }
  })
}
