import { expect, type Locator, type Page } from '@playwright/test'
import fs from 'node:fs/promises'
import path from 'node:path'
import { test } from './workspace-fixture'

const longLabel = 'A long fixture label for checking truncation and selected filter containment '.repeat(3).slice(0, 200)
const sizes = [{ width: 1440, height: 900 }, { width: 390, height: 844 }, { width: 375, height: 667 }]
const statesDirectory = process.env.FLOW_FILTER_STATE_SHOTS

async function capture(page: Page, name: string, target: Locator) {
  if (!statesDirectory) return
  await fs.mkdir(statesDirectory, { recursive: true })
  await page.evaluate(() => document.fonts.ready)
  const state = await target.evaluate(element => {
    const style = getComputedStyle(element), box = element.getBoundingClientRect()
    return { text: element.textContent, rect: { x: box.x, y: box.y, width: box.width, height: box.height }, focused: element === document.activeElement, focusVisible: element.matches(':focus-visible'), disabled: element.getAttribute('aria-disabled'), selected: element.getAttribute('aria-checked'), color: style.color, background: style.backgroundColor, outline: style.outline, shadow: style.boxShadow }
  })
  await fs.writeFile(path.join(statesDirectory, `${name}.json`), JSON.stringify(state, null, 2))
  const box = (await target.boundingBox())!, viewport = page.viewportSize()!
  const x = Math.max(0, box.x - 8), y = Math.max(0, box.y - 8)
  await page.screenshot({ path: path.join(statesDirectory, `${name}.png`), clip: { x, y, width: Math.min(viewport.width, box.x + box.width + 8) - x, height: Math.min(viewport.height, box.y + box.height + 8) - y }, animations: 'disabled' })
}

async function capturePanels(page: Page, name: string) {
  if (!statesDirectory) return
  const boxes = await page.locator('[data-variant=root]:visible, [data-level]:visible').evaluateAll(elements => elements.map(element => {
    const box = element.getBoundingClientRect()
    return { x: box.x, y: box.y, right: box.right, bottom: box.bottom }
  }))
  expect(boxes.length).toBeGreaterThan(0)
  const viewport = page.viewportSize()!
  const x = Math.max(0, Math.min(...boxes.map(box => box.x)) - 2)
  const y = Math.max(0, Math.min(...boxes.map(box => box.y)) - 2)
  const right = Math.min(viewport.width, Math.max(...boxes.map(box => box.right)) + 2)
  const bottom = Math.min(viewport.height, Math.max(...boxes.map(box => box.bottom)) + 2)
  await page.screenshot({ path: path.join(statesDirectory, `${name}.png`), clip: { x, y, width: right - x, height: bottom - y }, animations: 'disabled' })
}

async function expectContained(page: Page) {
  const bounds = await page.locator('[data-variant=root]:visible, [data-level]:visible, [data-filter-chip]:visible, [role=tooltip]:visible').evaluateAll(elements => elements.map(element => {
    const box = element.getBoundingClientRect()
    return { x: box.x, right: box.right, y: box.y, bottom: box.bottom }
  }))
  const viewport = page.viewportSize()!
  for (const box of bounds) {
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.right).toBeLessThanOrEqual(viewport.width)
    expect(box.y).toBeGreaterThanOrEqual(0)
    expect(box.bottom).toBeLessThanOrEqual(viewport.height)
  }
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
}

async function closeMenus(page: Page) {
  for (let i = 0; i < 4 && await page.locator('[data-variant=root][data-state=open]').count(); i++) {
    await page.keyboard.press('Escape')
    await page.waitForTimeout(150)
  }
  await expect(page.locator('[data-variant=root]')).toBeHidden()
}

async function clearFilters(page: Page) {
  while (await page.locator('[data-filter-chip]').count()) await page.locator('[data-filter-chip]').first().locator(':scope > button').last().click()
}

async function expectLabelsFit(menu: Locator) {
  const labels = menu.locator('[class*="valueLabel"]')
  expect(await labels.count()).toBeGreaterThan(0)
  expect(await labels.evaluateAll(elements => elements.every(element => {
    const range = document.createRange(); range.selectNodeContents(element)
    return range.getBoundingClientRect().width <= element.getBoundingClientRect().width + .01
  }))).toBe(true)
}

for (const viewport of sizes) for (const colorScheme of ['dark', 'light'] as const) for (const locale of ['en-US', 'zh-CN'] as const) {
  test.describe(`${viewport.width} ${colorScheme} ${locale}`, () => {
    test.use({ viewport, colorScheme, locale, isMobile: viewport.width < 768, hasTouch: viewport.width < 768 })

    test('checks filter control states and text containment', async ({ page, request, workspaceKey }) => {
      test.setTimeout(90_000)
      const headers = { 'X-Workspace-Key': workspaceKey }
      const settings = await request.patch('/api/account/settings', { headers, data: { language: locale } })
      expect(settings.ok()).toBeTruthy()
      const data = await (await request.get('/api/bootstrap', { headers })).json() as { viewer: { id: string }; teams: Array<{ id: string; key: string }> }
      const team = data.teams[0]
      const labelIds: string[] = []
      for (const name of ['A', '标签', longLabel]) {
        const response = await request.post('/api/labels', { headers, data: { name, color: '#5e6ad2', resourceType: 'issue' } })
        expect(response.ok()).toBeTruthy()
        labelIds.push((await response.json() as { id: string }).id)
      }
      const issue = await request.post('/api/issues', { headers, data: { title: 'Filter control review', teamId: team.id, stateId: 'state_todo', assigneeId: data.viewer.id, labelIds } })
      expect(issue.ok()).toBeTruthy()
      // The auth-disabled server always rehydrates its fixed local identity.
      // Supply only that person's display text as a browser fixture.
      await page.route(/\/api\/(?:bootstrap|issue-records\/bootstrap)(?:\?|$)/, async route => {
        if (route.request().headers()['x-workspace-key'] !== workspaceKey) return route.continue()
        const response = await route.fetch()
        const bootstrap = await response.json()
        const visit = (value: unknown) => {
          if (!value || typeof value !== 'object') return
          const record = value as Record<string, unknown>
          if (record.id === data.viewer.id && 'displayName' in record) record.displayName = '林 舟'
          for (const child of Object.values(record)) visit(child)
        }
        visit(bootstrap)
        await route.fulfill({ response, json: bootstrap })
      })
      await page.addInitScript(savedLocale => localStorage.setItem('flow:locale', savedLocale), locale)
      const errors: string[] = []
      page.on('pageerror', error => errors.push(error.message))
      await page.goto(`/${workspaceKey}/team/${team.key}/all`)
      await page.evaluate(() => document.fonts.ready)
      await expect(page.locator('html')).toHaveAttribute('data-locale', locale)
      await expect(page.locator('html')).toHaveAttribute('data-theme', colorScheme)
      const addFilter = page.getByRole('button', { name: /^(Add filter|添加筛选|添加筛选条件|添加过滤器)$/ })
      await expect(addFilter).toBeVisible()
      const prefix = `${viewport.width}-${colorScheme}-${locale}`
      await capture(page, `${prefix}-resting`, addFilter)
      await addFilter.hover()
      await capture(page, `${prefix}-hover`, addFilter)
      await page.mouse.move(1, 1)
      await addFilter.focus()
      await page.keyboard.press('ArrowRight')
      await page.keyboard.press('ArrowLeft')
      await expect(addFilter).toBeFocused()
      expect(await addFilter.evaluate(element => element.matches(':focus-visible'))).toBe(true)
      await capture(page, `${prefix}-focus-visible`, addFilter)
      await addFilter.click()
      const root = page.locator('[data-variant=root]')
      await expect(root).toBeVisible()
      await expect(root).toHaveCSS('width', `${viewport.width < 768 ? viewport.width - 32 : 207}px`)
      await capture(page, `${prefix}-open-root`, root)
      let releaseRequest!: () => void
      const pending = new Promise<void>(resolve => { releaseRequest = resolve })
      let requests = 0
      await page.route('**/api/ai/issue-filter', async route => {
        requests += 1
        await pending
        await route.fulfill({ json: { filters: [{ field: 'status', option: { id: 'state_todo', label: 'Todo', kind: 'status', stateType: 'unstarted' } }] } })
      })
      await root.locator('input').fill('Filter review query')
      const disabled = root.locator('[cmdk-item][data-value^="__ai__"]')
      const enabledColor = await disabled.evaluate(element => getComputedStyle(element).color)
      await disabled.click()
      await expect(disabled).toHaveAttribute('aria-disabled', 'true')
      await expect(disabled).not.toHaveCSS('color', enabledColor)
      await capture(page, `${prefix}-disabled`, disabled)
      await disabled.click({ force: true })
      expect(requests).toBe(1)
      releaseRequest()
      await expect(root).toBeHidden()
      await page.unroute('**/api/ai/issue-filter')
      await clearFilters(page)
      await addFilter.click()
      const field = async (name: string) => {
        const option = root.locator(`[cmdk-item][data-value="${name}"]`)
        await option.scrollIntoViewIfNeeded()
        if (viewport.width < 768) await option.click()
        else await option.hover()
      }
      await field('Status')
      await expect(page.locator('[data-level="1"]')).toBeVisible()
      await expectContained(page)
      await capturePanels(page, `${prefix}-open-status`)
      await closeMenus(page)
      await addFilter.click()
      await field('Assignee')
      const person = page.getByRole('option', { name: /林 舟/ })
      await expect(person).toBeVisible()
      const avatar = person.locator('[class*="optionAvatar"]')
      await expect(avatar).toHaveText('林舟')
      const avatarFits = await avatar.evaluate(element => {
        const range = document.createRange(); range.selectNodeContents(element)
        const glyphs = range.getBoundingClientRect(), box = element.getBoundingClientRect()
        return glyphs.width <= box.width && glyphs.height <= box.height
      })
      expect(avatarFits).toBe(true)
      await capture(page, `${prefix}-cjk-avatar`, page.locator('[data-level="1"]'))
      await person.click()
      await expect(page.locator('[data-filter-chip]')).toContainText('林 舟')
      await addFilter.click()
      await field('Assignee')
      await expect(page.getByRole('option', { name: /林 舟/ })).toHaveAttribute('aria-checked', 'true')
      await capture(page, `${prefix}-selected-avatar`, page.locator('[data-level="1"]'))
      await closeMenus(page)
      await clearFilters(page)
      for (const [index, label] of ['A', '标签', longLabel].entries()) {
        await addFilter.click()
        await field('Labels')
        await page.locator('[data-level="1"] input').fill(label)
        const option = page.locator('[data-level="1"]').getByRole('option').filter({ has: page.getByText(label, { exact: true }) })
        await expect(option).toBeVisible()
        await option.click()
        await expectContained(page)
        const chip = page.locator('[data-filter-chip]')
        await expect(chip).toContainText(label)
        await capture(page, `${prefix}-selected-chip-${index}`, chip)
        const value = chip.locator(':scope > button').nth(1)
        await expect(value).toHaveAccessibleDescription(label)
        if (index === 2) {
          await expect(addFilter).toBeFocused()
          await value.scrollIntoViewIfNeeded()
          await page.mouse.move(1, 1)
          await value.hover()
          const tooltip = page.getByRole('tooltip').filter({ hasText: label })
          await expect(tooltip).toBeVisible()
          await expectContained(page)
          await capture(page, `${prefix}-long-label-tooltip`, tooltip)
          await page.mouse.move(1, 1)
          await expect(tooltip).toBeHidden()
        }
        await addFilter.click()
        await field('Labels')
        await expect(page.locator('[data-level="1"]').getByRole('option').filter({ has: page.getByText(label, { exact: true }) })).toHaveAttribute('aria-checked', 'true')
        await expectContained(page)
        if (index === 2) await capture(page, `${prefix}-selected-long-option`, page.locator('[data-level="1"]'))
        await closeMenus(page)
        await clearFilters(page)
      }
      await addFilter.click()
      await field('Customers')
      await expectLabelsFit(page.locator('[data-level="1"]'))
      const numberCategory = page.locator('[data-level="1"]').getByRole('option', { name: /^(Customer count|客户数量)$/ })
      if (viewport.width < 768) await numberCategory.click()
      else await numberCategory.hover()
      const numberMenu = page.locator('[data-number-input]')
      await expect(numberMenu).toBeVisible()
      await capture(page, `${prefix}-number-empty`, numberMenu)
      await numberMenu.getByRole('searchbox').fill('1000000')
      await expectLabelsFit(numberMenu)
      // Extreme values may ellipsize; comparison operators and accessible values must remain complete.
      await numberMenu.getByRole('searchbox').fill('9007199254740991')
      await expect(numberMenu.getByRole('option')).toHaveCount(4)
      for (const option of await numberMenu.getByRole('option').all()) await expect(option).toHaveAccessibleName(/9,007,199,254,740,991$/)
      expect(await numberMenu.locator('[class*="numberComparison"]').evaluateAll(elements => elements.every(element => {
        const box = element.getBoundingClientRect(), label = element.parentElement!.getBoundingClientRect()
        return box.x >= label.x && box.right <= label.right
      }))).toBe(true)
      await expectContained(page)
      await numberMenu.getByRole('searchbox').press('ArrowDown')
      await expect(numberMenu.getByRole('option').nth(1)).toHaveAttribute('aria-selected', 'true')
      await numberMenu.getByRole('option').nth(1).hover()
      await capture(page, `${prefix}-number-submenu`, numberMenu)
      await numberMenu.getByRole('option').nth(1).click()
      const numericChip = page.locator('[data-filter-chip]')
      await expect(numericChip).toContainText('9,007,199,254,740,991')
      await expectContained(page)
      await capture(page, `${prefix}-selected-number`, numericChip)
      const selectedValue = numericChip.locator(':scope > button').nth(1)
      await expect(selectedValue).toHaveAccessibleDescription('9,007,199,254,740,991')
      await expect(addFilter).toBeFocused()
      await selectedValue.scrollIntoViewIfNeeded()
      await page.mouse.move(1, 1)
      await selectedValue.hover()
      const numberTooltip = page.getByRole('tooltip').filter({ hasText: '9,007,199,254,740,991' })
      await expect(numberTooltip).toBeVisible()
      await expectContained(page)
      await capture(page, `${prefix}-value-hover`, numberTooltip)
      await selectedValue.focus()
      await page.keyboard.press('Tab')
      await page.keyboard.press('Shift+Tab')
      expect(await selectedValue.evaluate(element => element.matches(':focus-visible'))).toBe(true)
      await capture(page, `${prefix}-value-focus`, selectedValue)
      await selectedValue.click()
      const selectedNumber = page.getByRole('option', { name: '9,007,199,254,740,991', exact: true })
      await expect(selectedNumber).toBeVisible()
      expect(await selectedNumber.locator('[data-i18n-ignore]').evaluate(element => {
        const range = document.createRange(); range.selectNodeContents(element)
        return range.getBoundingClientRect().width <= element.getBoundingClientRect().width + .01
      })).toBe(true)
      await capture(page, `${prefix}-selected-number-open`, selectedNumber.locator('xpath=ancestor::*[@data-flow-motion="floating"]'))
      await page.keyboard.press('Escape')
      await clearFilters(page)
      await addFilter.click()
      await field('Dates')
      await expectLabelsFit(page.locator('[data-level="1"]'))
      const due = page.locator('[data-level="1"] [role=option]').filter({ hasText: /Due date|截止日期/ }).first()
      if (viewport.width < 768) await due.click()
      else await due.hover()
      await expect(page.locator('[data-level="2"]')).toBeVisible()
      await expectContained(page)
      await capture(page, `${prefix}-nested-submenu`, page.locator('[data-level="2"]'))
      expect(errors).toEqual([])
    })
  })
}
