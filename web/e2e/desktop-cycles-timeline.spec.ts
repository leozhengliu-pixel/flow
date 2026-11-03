import { expect, type Locator } from '@playwright/test'
import { test } from './workspace-fixture'
import type { BootstrapData, Issue } from '../src/types/flow'

test.skip(({ isMobile }) => isMobile, 'Desktop cycles timeline and details')
test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })

// A started cycle with estimated issues in three states, followed by two upcoming cycles.
async function seedCycles(request: import('@playwright/test').APIRequestContext, workspaceKey: string) {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const data = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const team = data.teams[0]
  expect((await request.patch(`/api/teams/${team.id}/settings`, { headers, data: { estimateType: 'fibonacci' } })).ok()).toBeTruthy()
  expect((await request.patch(`/api/teams/${team.id}/cycle-settings`, { headers, data: { enabled: true, autoCreate: true, durationWeeks: 2, cooldownWeeks: 1, upcomingCount: 2 } })).ok()).toBeTruthy()
  const boot = await (await request.get('/api/bootstrap', { headers })).json() as BootstrapData
  const current = boot.cycles.find(cycle => cycle.status === 'current')!
  const state = (type: string) => boot.states.find(item => item.type === type)!
  for (const [title, type, estimate] of [['[cycles] Todo', 'unstarted', 5], ['[cycles] Doing', 'started', 3], ['[cycles] Done', 'completed', 8]] as const) {
    const response = await request.post('/api/issues', { headers, data: { title, teamId: team.id, stateId: state(type).id, priority: 2, estimate, cycleId: current.id } })
    expect(response.status()).toBe(201)
    await response.json() as Issue
  }
  return { team, current, upcoming: boot.cycles.filter(cycle => cycle.status === 'upcoming') }
}

const offset = async (node: Locator, parent: Locator) => {
  const [a, b] = await Promise.all([node.boundingBox(), parent.boundingBox()])
  return { x: a!.x - b!.x, y: a!.y - b!.y, width: a!.width, height: a!.height }
}

test('lists cycles on a timeline: rail, status pill, capacity ring, and the current cycle with its chart and legend', async ({ page, request, workspaceKey }) => {
  const { team, current, upcoming } = await seedCycles(request, workspaceKey)
  await page.goto(`/${workspaceKey}/team/${team.key}/cycles`)
  const list = page.locator('.flow-cycle-list')
  const card = page.getByTestId(`cycle-timeline-${current.number}`)
  const next = page.getByTestId(`cycle-timeline-${upcoming[0].number}`)
  await expect(card).toBeVisible()
  expect((await card.boundingBox())!.height).toBe(288)
  expect((await next.boundingBox())!.height).toBe(70)
  // The cycle icon sits 126px in and 27px down; the title follows after a 16px gap.
  const icon = await offset(card.locator('.cycle-timeline-icon'), card)
  expect(icon).toMatchObject({ x: 126, y: 27, width: 16, height: 16 })
  expect((await offset(card.locator('.cycle-timeline-title'), card)).x).toBe(158)
  // Status: a 19.5px pill with a 2px radius; every row's columns line up.
  const pill = next.locator('.cycle-timeline-status')
  await expect(pill).toHaveCSS('height', '19.5px')
  await expect(pill).toHaveCSS('border-radius', '2px')
  await expect(pill).toHaveCSS('background-color', 'lch(7.32 0.85 272)')
  await expect(pill).toHaveCSS('padding', '0px 6px')
  expect((await card.locator('.cycle-timeline-status').boundingBox())!.x).toBe((await pill.boundingBox())!.x)
  expect((await card.locator('.cycle-timeline-capacity').boundingBox())!.x).toBe((await next.locator('.cycle-timeline-capacity').boundingBox())!.x)
  await expect(card.locator('.cycle-timeline-status')).toHaveText('Current')
  // The capacity is a 16px ring with a track and an arc, then "53% of capacity".
  const ring = await offset(card.locator('.cycle-capacity-ring'), card)
  expect(ring).toMatchObject({ width: 16, height: 16 })
  await expect(card.locator('.cycle-capacity-ring circle')).toHaveCount(2)
  await expect(card.locator('.cycle-timeline-capacity em')).toHaveText('of capacity')
  // The rail: a 1px line at 73.5px, a 9px dot on it, the date to its left.
  const rail = await card.evaluate(element => { const style = getComputedStyle(element, '::before'); return { left: style.left, width: style.width, top: style.top } })
  expect(rail).toEqual({ left: '73.5px', width: '1px', top: '4.5px' })
  const date = await offset(card.locator('.cycle-timeline-date'), card)
  expect(date).toMatchObject({ x: 0, y: 275, height: 29 })
  expect(date.width).toBeCloseTo(62.6, 1)
  // The chart opens at 136px, 58px down and 210px tall; the legend is 300px wide, 60px from the right.
  const chart = await offset(card.locator('.cycle-timeline-chart'), card)
  expect(chart).toMatchObject({ x: 136, y: 58, height: 210 })
  const legend = await offset(card.locator('.cycle-timeline-metrics'), card)
  expect(legend.width).toBe(300)
  expect((await card.boundingBox())!.width - legend.x - legend.width).toBe(60)
  expect(await card.locator('.cycle-timeline-metric').evaluateAll(items => items.map(item => item.getBoundingClientRect().height))).toEqual([43.5, 44.5, 44.5])
  // The axis starts with the cycle's first day and ends the day after its last.
  const axis = card.locator('.cycle-timeline-axis > span')
  expect(await axis.count()).toBeGreaterThanOrEqual(2)
  // Hovering a day reads its figures; a day that has not happened shows a dash.
  const canvas = card.locator('.cycle-chart__canvas')
  const box = (await canvas.boundingBox())!
  await page.mouse.move(box.x + box.width - 20, box.y + 60)
  await expect(card.locator('.cycle-chart__tooltip')).toBeVisible()
  await expect(card.locator('.cycle-timeline-metric strong').first()).toHaveText('-')
  await page.mouse.move(box.x - 40, box.y - 40)
  await expect(card.locator('.cycle-chart__tooltip')).toHaveCount(0)
  expect(list).toBeTruthy()
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/desktop-cycles-timeline.png`, animations: 'disabled' })
})

test('shows the cycle details card with Progress, one set of tabs and the cycle switcher icon', async ({ page, request, workspaceKey }) => {
  const { team, current } = await seedCycles(request, workspaceKey)
  await page.goto(`/${workspaceKey}/team/${team.key}/cycle/${current.number}`)
  const panel = page.locator('.cycle-details-card')
  await expect(panel).toBeVisible()
  const card = await panel.boundingBox()
  expect(card!.width).toBe(428)
  // Status and dates: a 23.5px chip with a 4px radius, and a 28px dates chip.
  const chip = panel.locator('.cycle-panel-status')
  await expect(chip).toHaveText('Current')
  await expect(chip).toHaveCSS('border-radius', '4px')
  expect((await chip.boundingBox())!.height).toBe(23.5)
  expect((await panel.locator('.cycle-panel-dates').boundingBox())!.height).toBe(28)
  // Title row: 16px icon, 15px/600 title, 28px favourite and 24px options buttons.
  await expect(panel.locator('.cycle-panel-heading h2')).toHaveCSS('font-size', '15px')
  expect((await panel.locator('.cycle-panel-favorite').boundingBox())!.width).toBe(28)
  expect((await panel.locator('.cycle-panel-heading .cycle-row-menu').boundingBox())!.width).toBe(24)
  const title = await offset(panel.locator('.cycle-panel-heading h2'), panel)
  // 57px below the card's outer edge; a hairline border may round to 1px at the emulated scale.
  expect(title.y).toBeGreaterThanOrEqual(57)
  expect(title.y).toBeLessThanOrEqual(57.5)
  // Progress: the legend reads in three columns 135.66px apart, then the chart, then ONE set of five tabs.
  const progress = panel.locator('.cycle-panel-progress')
  await expect(progress.locator('.cycle-panel-section__title span')).toHaveText('Progress')
  const columns = await progress.locator('.cycle-stat').evaluateAll(items => items.map(item => item.getBoundingClientRect().x))
  // Within half a pixel: a hairline border may round to 1px at the emulated scale and narrow the card.
  expect(columns[1] - columns[0]).toBeCloseTo(135.66, 0)
  expect(columns[2] - columns[1]).toBeCloseTo(135.66, 0)
  expect(await progress.locator('.cycle-stat-label').allTextContents()).toEqual(['Scope', 'Started', 'Completed'])
  const progressChart = (await progress.locator('.cycle-chart').boundingBox())!
  expect(progressChart.width).toBeGreaterThanOrEqual(402)
  expect(progressChart.width).toBeLessThanOrEqual(403)
  expect(progressChart.height).toBe(200)
  await expect(panel.getByRole('tablist')).toHaveCount(1)
  await expect(panel.getByRole('tab')).toHaveText(['Assignees', 'Labels', 'Priority', 'Projects', 'Teams'])
  expect((await panel.locator('.cycle-breakdown-tabs').boundingBox())!.height).toBe(32)
  await expect(panel.locator('.cycle-breakdown-row').first()).toHaveCSS('height', '42px')
  // Planning stays, without a second set of tabs.
  await expect(panel.locator('.cycle-planning .cycle-panel-section__title span')).toHaveText('Planning')
  await expect(panel.locator('.cycle-planning [role="tab"]')).toHaveCount(0)
  // The breadcrumb shows the cycle icon and a chevron: a 16px icon, 8px gap, name, 5px gap, 8x9 chevron.
  await expect(page.locator('.cycle-switcher-play')).toBeVisible()
  expect(await page.locator('.cycle-switcher-play').boundingBox()).toMatchObject({ width: 16, height: 16 })
  expect(await page.locator('.cycle-switcher-chevron').boundingBox()).toMatchObject({ width: 8, height: 9 })
  await expect(page.locator('.cycle-switcher')).toHaveCSS('border-radius', '6px')
  // Collapsing Progress hides its tabs and chart.
  await progress.getByRole('button', { name: 'Collapse progress section' }).click()
  await expect(progress.locator('.cycle-chart')).toHaveCount(0)
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/desktop-cycle-details.png`, animations: 'disabled' })
})
