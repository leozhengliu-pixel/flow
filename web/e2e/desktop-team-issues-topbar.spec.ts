import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.skip(({ isMobile }) => isMobile, 'desktop-only top bar parity')

test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })

test('keeps the desktop issue filter at the window edge and applies a status', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as { teams: Array<{ id: string; key: string }> }
  const team = data.teams[0]
  await request.post('/api/issues', { headers, data: { title: 'Filter geometry sample', teamId: team.id, stateId: 'state_todo' } })
  await page.goto(`/${workspaceKey}/team/${team.key}/all`)
  await page.getByRole('button', { name: 'Add filter', exact: true }).click()
  const menu = page.locator('[data-layout=issues][data-variant=root]')
  await expect(menu).toBeVisible()
  await expect(menu).toHaveCSS('width', '207px')
  await expect.poll(async () => {
    const box = await menu.boundingBox()
    return box ? 1440 - box.x - box.width : -1
  }).toBe(11)
  const search = menu.getByRole('combobox', { name: 'Add Filter…', exact: true })
  await expect(search).toBeInViewport({ ratio: 1 })
  await expect(search).toBeEditable()
  await expect(search).toHaveAttribute('placeholder', 'Add Filter…')
  await page.evaluate(() => document.fonts.ready)
  expect(await search.evaluate(input => {
    const style = getComputedStyle(input)
    const context = document.createElement('canvas').getContext('2d')!
    context.font = style.font
    const availableWidth = input.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight)
    return context.measureText(input.getAttribute('placeholder')!).width <= availableWidth
  })).toBe(true)
  const hint = menu.locator('kbd')
  await expect(hint).toBeVisible()
  const searchBox = (await search.boundingBox())!
  const hintBox = (await hint.boundingBox())!
  expect(searchBox.x + searchBox.width).toBeLessThanOrEqual(hintBox.x)
  // The grid may size the input differently across browsers; its text must fit and filter the menu.
  await search.fill('Status')
  expect(await search.evaluate(input => input.scrollWidth <= input.clientWidth)).toBe(true)
  await expect(menu.locator('[cmdk-item][data-value=Status]')).toBeVisible()
  await expect(menu.locator('[cmdk-item][data-value=Priority]')).toHaveCount(0)
  await menu.locator('[cmdk-item][data-value=Status]').hover()
  const choices = page.getByRole('listbox', { name: 'Status', exact: true })
  await expect(choices).toBeVisible()
  const rootBox = (await menu.boundingBox())!
  const valueBox = (await page.locator('[data-level="1"]').boundingBox())!
  expect(valueBox.x + valueBox.width - rootBox.x).toBe(2)
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/desktop-filter.png`, animations: 'disabled' })
  await choices.getByRole('option', { name: /Todo/ }).click()
  await expect(menu).toBeHidden()
  await expect(page.locator('[data-filter-chip]')).toContainText('Todo')
})

test('shapes desktop identifiers with capital spacing and tabular digits', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const response = await request.get('/api/bootstrap', { headers })
  const data = await response.json() as { teams: Array<{ id: string; key: string }> }
  const team = data.teams[0]
  const issue = await request.post('/api/issues', { headers, data: { title: 'Font metrics sample', teamId: team.id } })
  expect(issue.status()).toBe(201)
  await page.goto(`/${workspaceKey}/team/${team.key}/all`)
  const identifier = page.locator('[data-issue-row-id] [class*="identifier"]').first()
  await expect(identifier).toBeInViewport({ ratio: 1 })
  await expect(identifier).toHaveCSS('font-feature-settings', '"calt", "cpsp", "tnum"')
  await expect(identifier).toHaveCSS('font-variant-numeric', 'tabular-nums')
  await expect(identifier).toHaveCSS('letter-spacing', '-0.26px')
  await page.evaluate(() => document.fonts.ready)
  const widths = await identifier.evaluate(element => {
    const probe = element.cloneNode(false) as HTMLElement
    probe.style.cssText = 'position:fixed;width:max-content;max-width:none'
    element.parentElement!.append(probe)
    const widths = ['FLO-53', 'FLO-11', 'FLO-99', 'PIC-2'].map(text => {
      probe.textContent = text
      return probe.getBoundingClientRect().width
    })
    probe.remove()
    return widths
  })
  // Glyph advances differ across platforms; tabular digits must still align.
  for (const width of widths.slice(1, 3)) expect(width).toBeCloseTo(widths[0], 3)
  expect(widths[3]).toBeLessThan(widths[0])
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/desktop-identifiers.png`, animations: 'disabled' })
})

test('keeps the team Issues top bar controls, view pills and tool group at the desktop metrics', async ({ page, request, workspaceKey }) => {
  const response = await request.get('/api/bootstrap', { headers: { 'X-Workspace-Key': workspaceKey } })
  expect(response.ok()).toBeTruthy()
  const workspace = await response.json() as { teams?: Array<{ key: string }> }
  const teamKey = workspace.teams?.[0]?.key
  expect(teamKey).toBeTruthy()

  await page.goto(`/${workspaceKey}/team/${teamKey}/all`)
  const header = page.locator('[data-content-view-header]')
  await expect(header.getByRole('switch', { name: 'Add to favorites' })).toBeVisible()
  await expect(header.getByRole('link', { name: 'Setup team notifications' })).toBeVisible()
  await expect(header.getByRole('button', { name: 'New issue', exact: true })).toBeVisible()
  await expect(header.locator('[data-current]')).toHaveCSS('color', 'lch(90.451 1.2 272)')
  await expect(header.locator('[data-content-view-breadcrumb] a>span:last-child')).toHaveCSS('color', 'lch(90.451 1.2 272)')
  await expect(page.locator('[data-content-view-subheader]')).toHaveCSS('gap', '6px')

  const geometry = await page.evaluate(() => {
    const rect = (element: Element | null) => {
      if (!element) return null
      const box = element.getBoundingClientRect()
      const style = getComputedStyle(element)
      return {
        x: box.x, y: box.y, width: box.width, height: box.height,
        fontSize: style.fontSize, fontWeight: style.fontWeight,
        padding: style.padding, lineHeight: style.lineHeight,
      }
    }
    const header = document.querySelector('[data-content-view-header]')!
    const icon = document.querySelector('[data-content-view-breadcrumb] a > span:first-child')
    const star = document.querySelector('[data-content-view-header] button[aria-label="Add to favorites"]')
    const bell = document.querySelector('[data-content-view-header] [aria-label="Setup team notifications"]')
    const create = [...document.querySelectorAll('[data-content-view-header] button')].find(button => button.textContent?.trim() === 'New issue') ?? null
    const pills = [...document.querySelectorAll('[data-content-view-subheader] nav a')].slice(0, 3)
    const tools = [...document.querySelectorAll('[data-content-view-subheader] [data-toolbar-buttons-navigation] button')]
      .map(button => ({ label: button.getAttribute('aria-label') ?? '', rect: { x: button.getBoundingClientRect().x, width: button.getBoundingClientRect().width } }))
      .sort((a, b) => a.rect.x - b.rect.x)
    return {
      header: { ...rect(header)!, paddingLeft: getComputedStyle(header.querySelector('[data-header-row]')!).paddingLeft },
      icon: rect(icon),
      star: rect(star),
      bell: rect(bell),
      create: rect(create),
      pills: pills.map(pill => rect(pill)!),
      tools,
    }
  })

  expect(geometry.header.paddingLeft).toBe('8px')
  expect(geometry.icon?.width).toBe(16)
  expect(geometry.icon?.height).toBe(16)
  expect(geometry.star).toMatchObject({ width: 28, height: 28 })
  expect(geometry.bell).toMatchObject({ width: 28, height: 28 })
  expect(geometry.create).toMatchObject({ height: 28, fontSize: '12px', fontWeight: '500', padding: '0px 10px 0px 8px' })
  expect(geometry.create!.width).toBeGreaterThan(90)

  for (let index = 1; index < geometry.pills.length; index += 1) {
    const previous = geometry.pills[index - 1]
    expect(geometry.pills[index].x - (previous.x + previous.width)).toBeCloseTo(4, 1)
  }

  expect(geometry.tools.map(tool => tool.label)).toEqual(['Open insights', 'Add filter', 'Display options', 'Open details'])
  for (const tool of geometry.tools) expect(tool.rect.width).toBe(28)
  for (let index = 1; index < geometry.tools.length; index += 1) {
    const previous = geometry.tools[index - 1].rect
    expect(geometry.tools[index].rect.x - (previous.x + previous.width)).toBeCloseTo(6, 1)
  }
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/desktop-topbar.png`, animations: 'disabled' })

  const favorite = header.getByRole('switch', { name: 'Add to favorites' })
  await favorite.click()
  await expect(header.getByRole('switch', { name: 'Remove from favorites' })).toHaveAttribute('aria-checked', 'true')
  await header.getByRole('switch', { name: 'Remove from favorites' }).click()
  await expect(header.getByRole('switch', { name: 'Add to favorites' })).toHaveAttribute('aria-checked', 'false')

  await header.getByRole('button', { name: 'New issue', exact: true }).click()
  await expect(page.locator('[data-flow-motion="create"]')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.locator('[data-flow-motion="create"]')).toHaveCount(0)

  await header.getByRole('link', { name: 'Setup team notifications' }).click()
  await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/settings/teams/${teamKey}/notifications$`))
})
