import { expect, test } from '@playwright/test'

test.skip(({ isMobile }) => isMobile, 'desktop-only top bar parity')

test.use({ viewport: { width: 1440, height: 900 } })

test('keeps the team Issues top bar controls, view pills and tool group at the desktop metrics', async ({ page, request }, testInfo) => {
  const workspaceKey = `desktop-topbar-${testInfo.project.name}-r${testInfo.retry}`
  const response = await request.post('http://127.0.0.1:4180/api/workspaces', {
    data: { name: 'Desktop top bar parity', urlKey: workspaceKey, region: 'us', tutorial: false },
  })
  expect(response.status()).toBe(201)
  const workspace = await response.json() as { teams?: Array<{ key: string }> }
  const teamKey = workspace.teams?.[0]?.key
  expect(teamKey).toBeTruthy()

  await page.goto(`/${workspaceKey}/team/${teamKey}/all`)
  const header = page.locator('[data-content-view-header]')
  await expect(header.getByRole('switch', { name: 'Add to favorites' })).toBeVisible()
  await expect(header.getByRole('link', { name: 'Setup team notifications' })).toBeVisible()
  await expect(header.getByRole('button', { name: 'New issue', exact: true })).toBeVisible()

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
      header: { ...rect(header)!, paddingLeft: getComputedStyle(header).paddingLeft },
      icon: rect(icon),
      star: rect(star),
      bell: rect(bell),
      create: rect(create),
      pills: pills.map(pill => rect(pill)!),
      tools,
    }
  })

  expect(geometry.header.paddingLeft).toBe('18px')
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
