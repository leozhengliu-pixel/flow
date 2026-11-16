import { expect, type APIRequestContext, type Locator, type Page } from '@playwright/test'
import { test } from './workspace-fixture'
import { bootstrap, preferences } from './review-findings-fixture'

// A label group row (it opens a submenu of the group's labels) keeps its icon, name and submenu marker on one line in
// every labels picker, desktop and phone: a long Chinese group name ellipsizes instead of wrapping under the icon.
const longGroup = '事务类型与优先级分类标签组（客户可见，含历史迁移数据与外部同步来源）'
const phoneUserAgent = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1'

async function seed(request: APIRequestContext, workspaceKey: string) {
  const headers = { 'X-Workspace-Key': workspaceKey }
  await preferences(request, workspaceKey, 'Light', 'zh-CN')
  const data = await bootstrap(request, workspaceKey)
  const group = await (await request.post('/api/label-groups', { headers, data: { name: longGroup, color: '#26b5ce', resourceType: 'issue' } })).json() as { id: string }
  for (const name of ['缺陷', '功能请求']) expect((await request.post('/api/labels', { headers, data: { name, color: '#26b5ce', groupId: group.id } })).status()).toBe(201)
  expect((await request.post('/api/labels', { headers, data: { name: '新手入门', color: '#4cb782' } })).status()).toBe(201)
  const response = await request.post('/api/issues', { headers, data: { title: '标签分组', teamId: data.teams[0].id, stateId: data.states.find(state => state.type === 'unstarted')!.id } })
  expect(response.status()).toBe(201)
  return await response.json() as { identifier: string }
}

async function settle(menu: Locator) {
  await menu.evaluate(element => Promise.all(element.getAnimations({ subtree: true }).map(animation => animation.finished)))
}

/** The row's icon, name and (when shown) marker are vertically centred on the row, and the name is one ellipsized line. */
async function expectOneLine(row: Locator, { marker }: { marker: boolean }) {
  const geometry = await row.evaluate(element => {
    const box = (node: Element | null) => { if (!node) return null; const rect = node.getBoundingClientRect(); return rect.width ? { top: rect.top, height: rect.height, centre: rect.top + rect.height / 2, left: rect.left, right: rect.right } : null }
    const name = element.querySelector<HTMLElement>('.property-command-group-copy > .property-command-label')!
    const style = getComputedStyle(name)
    return {
      row: box(element)!,
      icon: box(element.querySelector('.property-command-icon svg')),
      name: box(name),
      marker: box(element.querySelector('.property-command-group-chevron')),
      whiteSpace: style.whiteSpace,
      textOverflow: style.textOverflow,
      truncated: name.scrollWidth > name.clientWidth,
      lineHeight: parseFloat(style.lineHeight) || parseFloat(style.fontSize) * 1.25,
    }
  })
  const centre = geometry.row.centre
  expect(Math.abs(geometry.icon!.centre - centre)).toBeLessThanOrEqual(1)
  expect(Math.abs(geometry.name!.centre - centre)).toBeLessThanOrEqual(1)
  expect(geometry.name!.height).toBeLessThan(geometry.lineHeight * 1.5)
  expect(geometry.name!.left).toBeGreaterThan(geometry.icon!.right)
  expect(geometry.whiteSpace).toBe('nowrap')
  expect(geometry.textOverflow).toBe('ellipsis')
  expect(geometry.truncated).toBe(true)
  if (marker) {
    expect(Math.abs(geometry.marker!.centre - centre)).toBeLessThanOrEqual(1)
    expect(geometry.marker!.left).toBeGreaterThan(geometry.name!.right)
    expect(geometry.row.right - geometry.marker!.right).toBe(18)
  } else expect(geometry.marker).toBeNull()
}

async function openIssueLabels(page: Page, workspaceKey: string, identifier: string) {
  await page.goto(`/${workspaceKey}/issue/${identifier}`)
  await page.locator('.labels-picker .label-project-trigger:visible').first().click()
}

test.describe('desktop', () => {
  test.use({ viewport: { width: 1440, height: 900 } })

  test('the issue labels picker keeps a long Chinese group name on one row', async ({ page, request, workspaceKey }) => {
    const issue = await seed(request, workspaceKey)
    await openIssueLabels(page, workspaceKey, issue.identifier)
    const menu = page.locator('.property-command-surface.property-command-labels')
    const row = menu.locator('.property-command-label-group-option')
    await expect(row).toHaveCount(1)
    await settle(menu)
    await expectOneLine(row, { marker: true })
    // The panel hugs the long row up to its 399px cap.
    expect((await menu.boundingBox())!.width).toBe(399)
    // The group's labels open beside the row.
    await row.hover()
    const submenu = page.locator('.property-command-label-submenu')
    await expect(submenu.getByRole('option')).toHaveText(['缺陷', '功能请求'])
  })
})

test.describe('desktop menus', () => {
  test.use({ viewport: { width: 1440, height: 900 } })

  test('the command menu, filter menu and bulk labels show a label group on one row', async ({ page, request, workspaceKey }) => {
    const issue = await seed(request, workspaceKey)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const data = await bootstrap(request, workspaceKey)
    const defect = data.labels.find(label => label.name === '缺陷')!
    expect((await request.post('/api/issues', { headers, data: { title: '标签分组 2', teamId: data.teams[0].id, stateId: data.states.find(state => state.type === 'unstarted')!.id, labelIds: [defect.id] } })).status()).toBe(201)

    // ⌘K → Add labels: the group is one row (checkbox track, icon at 40px, name at 64px) that opens its own step.
    await page.goto(`/${workspaceKey}/issue/${issue.identifier}`)
    await page.keyboard.press('ControlOrMeta+k')
    await page.locator('[cmdk-input]').fill('labels')
    await page.locator('[cmdk-item]').filter({ hasText: '添加标签' }).first().click()
    const groupRow = page.locator('[cmdk-item][data-label-command=group]').filter({ hasText: longGroup })
    await expect(groupRow).toHaveCount(1)
    const cells = await groupRow.evaluate(row => { const box = row.getBoundingClientRect(); return [...row.querySelectorAll('.label-command-icon, .label-command-name')].map(cell => { const rect = cell.getBoundingClientRect(); return { x: rect.x - box.x, centre: rect.y + rect.height / 2 - (box.y + box.height / 2), height: rect.height } }) })
    expect(cells.map(cell => cell.x)).toEqual([40, 64])
    for (const cell of cells) expect(Math.abs(cell.centre)).toBeLessThanOrEqual(1)
    expect(cells[1].height).toBeLessThan(20)
    await groupRow.click()
    await expect(page.locator('[cmdk-item]')).toHaveText(['缺陷', '功能请求'])
    await page.keyboard.press('Escape')

    // Filter menu → Labels: a label of a group reads "Group › Label", 8px either side of the 5px marker.
    await page.goto(`/${workspaceKey}/team/${data.teams[0].key}/all`)
    await page.getByRole('button', { name: /^添加筛选/ }).first().click()
    await page.getByRole('option', { name: '标签', exact: true }).hover()
    const value = page.getByRole('option').filter({ hasText: '缺陷' }).first()
    await expect(value).toBeVisible()
    await page.evaluate(() => Promise.all(document.getAnimations().map(animation => animation.finished)))
    const path = await value.evaluate(row => { const box = row.getBoundingClientRect(); const x = (selector: string) => row.querySelector(selector)!.getBoundingClientRect().x - box.x; const group = row.querySelector('[class*=valueGroup]:not(svg)')!.getBoundingClientRect(); return { group: x('[class*=valueGroup]:not(svg)'), marker: x('svg[class*=valueGroupChevron]'), groupEnd: group.right - box.x, label: x('[class*=valueGroupLabel]') } })
    expect(path.group).toBe(60)
    expect(path.marker - path.groupEnd).toBeCloseTo(8, 0)
    expect(path.label - path.marker).toBeCloseTo(13, 0)
    await page.keyboard.press('Escape')

    // Bulk: select both issues; the labels step shows the group with a mixed box naming 缺陷.
    const rows = page.locator('[data-issue-row-id]')
    await expect(rows).toHaveCount(2)
    for (const row of await rows.all()) { await row.hover(); await row.locator('input[type=checkbox], [role=checkbox]').first().click({ force: true }) }
    const bulk = page.locator('[data-my-issues-bulk-bar]')
    await expect(bulk).toContainText('2')
    await bulk.getByRole('button', { name: '打开命令菜单' }).click()
    await page.locator('[cmdk-item]').filter({ hasText: /更改或添加标签|Change or add labels/ }).first().click()
    const bulkGroup = page.locator('[cmdk-item][data-label-command=group]').filter({ hasText: longGroup })
    await expect(bulkGroup).toHaveAttribute('aria-checked', 'mixed')
    await expect(bulkGroup.locator('.label-command-detail')).toHaveText('缺陷')
  })
})

test.describe('phone', () => {
  test.use({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true, userAgent: phoneUserAgent })

  test('the issue labels sheet keeps a long Chinese group name on one row', async ({ page, request, workspaceKey }) => {
    const issue = await seed(request, workspaceKey)
    await openIssueLabels(page, workspaceKey, issue.identifier)
    const menu = page.locator('.phone-menu-surface.property-command-labels')
    const row = menu.locator('.property-command-label-group-option')
    await expect(row).toHaveCount(1)
    await settle(menu)
    await expectOneLine(row, { marker: false })
    // Tapping the row replaces the sheet with the group's labels: a search field named after the group, then its labels (no Back row).
    await row.tap()
    const submenu = page.locator('.property-command-label-submenu[data-phone-submenu]')
    await expect(submenu.getByRole('option')).toHaveText(['缺陷', '功能请求'])
    await settle(submenu)
    const box = (await submenu.boundingBox())!
    expect(box.x).toBe(16)
    expect(box.width).toBe(358)
    await expect(submenu.getByRole('textbox')).toHaveAttribute('placeholder', longGroup)
    await expect(submenu.getByRole('button', { name: '返回' })).toHaveCount(0)
    await expect(menu).toBeHidden()
    const choice = submenu.getByRole('option', { name: '缺陷' })
    const dot = (await choice.locator('.option-dot').boundingBox())!, name = (await choice.locator('.property-command-label').boundingBox())!, option = (await choice.boundingBox())!
    expect(dot.x - option.x).toBe(15.5)
    expect(name.x - option.x).toBe(36)
    // Choosing a label of the group closes the picker.
    await choice.tap()
    await expect(submenu).toBeHidden()
    await expect(menu).toBeHidden()
  })

  test('the new issue labels sheet keeps a long Chinese group name on one row', async ({ page, request, workspaceKey }) => {
    await seed(request, workspaceKey)
    await page.goto(`/${workspaceKey}/my-issues/assigned`)
    await page.getByRole('button', { name: '新建事项', exact: true }).click()
    await page.locator('[role=dialog] [data-property=labels]').click()
    const menu = page.locator('.phone-menu-surface.property-command-labels')
    const row = menu.locator('.property-command-label-group-option')
    await expect(row).toHaveCount(1)
    await settle(menu)
    await expectOneLine(row, { marker: false })
  })
})
