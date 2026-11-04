import { expect, type APIRequestContext, type Page } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData } from '../src/types/flow'

test.skip(({ isMobile }) => isMobile, 'Desktop timeline geometry')
test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })
test.setTimeout(90_000)

const LONG_NAME = 'Long running platform migration project with a very long name'
const NAMES_COLUMN = 320
const DAY = 86_400_000

const isoDay = (offset: number) => new Date(Date.now() + offset * DAY).toISOString().slice(0, 10)

async function seedProjects(request: APIRequestContext, workspaceKey: string) {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const teamIds = [data.teams[0].id]
  // The long project began more than a year ago, so at the default zoom its bar starts far left of the pane.
  for (const project of [
    { name: 'Alpha launch', startDate: isoDay(-20), targetDate: isoDay(60) },
    { name: 'Beta rollout', startDate: isoDay(30), targetDate: isoDay(150) },
    { name: LONG_NAME, startDate: isoDay(-420), targetDate: isoDay(120) },
  ]) expect((await request.post('/api/projects', { headers, data: { ...project, teamIds } })).ok()).toBeTruthy()
}

async function openTimeline(page: Page, workspaceKey: string, { grouping = 'No grouping', zoom = 'quarter' } = {}) {
  const display = { grouping, groupOrder: 'asc', layout: 'timeline', onlyLeadTeamProjects: false, ordering: 'Name', orderingDirection: 'asc', properties: ['Priority', 'Status', 'Health', 'Lead'], showClosed: 'All', showEmptyGroups: false, subGrouping: 'No grouping', timelineZoom: zoom }
  await page.addInitScript(([key, value]) => localStorage.setItem(key, value), [`flow:projects:view:${workspaceKey}:workspace:all`, JSON.stringify(display)])
  await page.goto(`/${workspaceKey}/projects/all`)
  const timeline = page.getByRole('region', { name: 'Project timeline', exact: true })
  await expect(timeline.locator('.lp-project-timeline__bar')).toHaveCount(3, { timeout: 30_000 })
  return timeline
}

test('keeps the axis legible and measured like the reference at every zoom', async ({ page, request, workspaceKey }) => {
  await seedProjects(request, workspaceKey)
  for (const zoom of ['week', 'month', 'quarter', 'year']) {
    const timeline = await openTimeline(page, workspaceKey, { zoom })
    await expect(timeline).toHaveAttribute('data-zoom', zoom)
    const labels = timeline.locator('.lp-project-timeline__minor > span > b')
    await expect(labels.first()).toHaveCSS('font-size', '11px')
    await expect(labels.first()).toHaveCSS('font-weight', '500')
    await expect(timeline.locator('.lp-project-timeline__header')).toHaveCSS('border-bottom-width', '0px')
    // Day numbers are centred on their tick and never crowd each other.
    const boxes = (await labels.evaluateAll(nodes => nodes.map(node => { const { x, width } = node.getBoundingClientRect(); return { x, width } }))).sort((left, right) => left.x - right.x)
    expect(boxes.length).toBeGreaterThan(10)
    for (let index = 1; index < boxes.length; index += 1) {
      const gap = boxes[index].x - (boxes[index - 1].x + boxes[index - 1].width)
      expect(gap, `${zoom}: gap before label ${index}`).toBeGreaterThanOrEqual(16)
    }
    // Ticks outnumber labels once the week pitch is tight, and every tick is centred on its label.
    const ticks = await timeline.locator('.lp-project-timeline__minor > span').count()
    if (zoom === 'quarter' || zoom === 'year') expect(ticks).toBeGreaterThan(boxes.length)
    // A month's name starts at its boundary instead of sticking to the pane edge.
    await expect(timeline.locator('.lp-project-timeline__major > span > b').first()).toHaveCSS('position', 'static')
  }
})

test('keeps a bar name readable when its bar starts under the names column', async ({ page, request, workspaceKey }) => {
  await seedProjects(request, workspaceKey)
  const timeline = await openTimeline(page, workspaceKey)
  const scroller = (await timeline.locator('.lp-project-timeline__scroller').boundingBox())!
  const namesRight = scroller.x + NAMES_COLUMN
  const bar = timeline.locator('.lp-project-timeline__bar').filter({ has: page.getByRole('button', { name: `${LONG_NAME} timeline bar`, exact: true }) })
  const barBox = (await bar.boundingBox())!
  expect(barBox.x, 'the bar starts left of the names column').toBeLessThan(namesRight)
  const label = bar.locator('.lp-project-timeline__bar-label')
  await expect(label).toHaveText(LONG_NAME)
  const labelBox = (await label.boundingBox())!
  expect(labelBox.x).toBeGreaterThanOrEqual(namesRight - 0.5)
  expect(labelBox.x + labelBox.width).toBeLessThanOrEqual(barBox.x + barBox.width + 0.5)
  // The label is what the pointer sees at its centre, not the names column.
  const topElement = await page.evaluate(([x, y]) => document.elementFromPoint(x, y)?.className ?? '', [labelBox.x + 20, labelBox.y + labelBox.height / 2])
  expect(String(topElement)).not.toContain('names-backdrop')
  // The name sits just above its bar and the bar itself is not pushed down.
  const buttonBox = (await bar.getByRole('button').first().boundingBox())!
  expect(buttonBox.y).toBeCloseTo(barBox.y, 1)
  expect(labelBox.y + labelBox.height).toBeLessThanOrEqual(barBox.y)
  // Scrolling right moves the label with the visible part of the bar.
  const before = labelBox.x
  await timeline.locator('.lp-project-timeline__scroller').evaluate(node => { node.scrollLeft += 200 })
  await expect.poll(async () => (await label.boundingBox())!.x).toBeGreaterThanOrEqual(namesRight - 0.5)
  expect((await label.boundingBox())!.x).toBeCloseTo(before, 0)
})

test('shows group rows only for a real grouping and keeps the names column whole', async ({ page, request, workspaceKey }) => {
  await seedProjects(request, workspaceKey)
  let timeline = await openTimeline(page, workspaceKey)
  await expect(timeline.locator('.lp-project-timeline__group')).toHaveCount(0)
  // Names column: 320px wide, icon + name, then health, status, priority and lead buttons, 6px apart.
  const rows = timeline.locator('.lp-project-timeline__row')
  await expect(rows).toHaveCount(3)
  const row = rows.first()
  await expect(row.locator('.lp-project-timeline__label')).toHaveCSS('width', `${NAMES_COLUMN}px`)
  const cells = row.locator('.lp-project-timeline__cells > button')
  await expect(cells).toHaveCount(4)
  const cellBoxes = await cells.evaluateAll(nodes => nodes.map(node => { const { x, width, height } = node.getBoundingClientRect(); return { x, width, height } }))
  for (const [index, box] of cellBoxes.entries()) {
    expect(box.width).toBe(28)
    expect(box.height).toBe(28)
    if (index) expect(box.x - (cellBoxes[index - 1].x + 28)).toBe(6)
  }
  const nameBox = (await row.locator('.lp-project-timeline__name > button').boundingBox())!
  expect(cellBoxes[0].x - (nameBox.x + nameBox.width)).toBe(6)
  // The long name truncates in the names column instead of pushing the cells away.
  const longRow = rows.filter({ hasText: LONG_NAME })
  await expect(longRow.locator('.lp-project-timeline__name > button > span').last()).toHaveCSS('text-overflow', 'ellipsis')
  expect((await longRow.locator('.lp-project-timeline__cells').boundingBox())!.x).toBeCloseTo(cellBoxes[0].x, 0)

  timeline = await openTimeline(page, workspaceKey, { grouping: 'Status' })
  const group = timeline.locator('.lp-project-timeline__group h2').first()
  await expect(group).toBeVisible()
  const scroller = (await timeline.locator('.lp-project-timeline__scroller').boundingBox())!
  const groupBox = (await group.boundingBox())!
  expect(groupBox.x).toBeCloseTo(scroller.x, 0)
  expect(groupBox.width).toBeCloseTo(NAMES_COLUMN, 0)
  // The group's name is on top of the names column backdrop.
  expect(await page.evaluate(([x, y]) => document.elementFromPoint(x, y)?.closest('h2') !== null, [groupBox.x + 20, groupBox.y + groupBox.height / 2])).toBe(true)
})

test('draws the names column down the pane and fades the today line', async ({ page, request, workspaceKey }) => {
  await seedProjects(request, workspaceKey)
  const timeline = await openTimeline(page, workspaceKey)
  const scroller = (await timeline.locator('.lp-project-timeline__scroller').boundingBox())!
  const backdrop = (await timeline.locator('.lp-project-timeline__names-backdrop').boundingBox())!
  expect(backdrop.width).toBe(NAMES_COLUMN)
  expect(backdrop.y + backdrop.height).toBeCloseTo(scroller.y + scroller.height, 0)
  await expect(timeline.locator('.lp-project-timeline__names-backdrop')).toHaveCSS('background-color', 'lch(5.52 0.4 272)')
  const bar = timeline.locator('.lp-project-timeline__bar > button').first()
  await expect(bar).toHaveCSS('background-image', /lch\(11\.74 2\.22 272\.7\)/)
  await expect(bar).toHaveCSS('border-top-color', 'lch(27.12 1.48 272)')
  await expect(bar).toHaveCSS('box-shadow', /0\.3\)? 0px 0\.5px 1px 1px/)
  await expect(timeline.locator('.lp-project-timeline__today-line')).toHaveCSS('background-image', /linear-gradient/)
})
