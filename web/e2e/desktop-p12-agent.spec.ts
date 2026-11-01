import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark', launchOptions: { args: ['--force-device-scale-factor=2', '--window-size=1440,900'] } })
test.skip(({ isMobile }) => isMobile, 'Desktop agent audit')

test.beforeEach(async ({ page, context }) => {
  const session = await context.newCDPSession(page)
  await session.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 0, mobile: false })
  expect(await page.evaluate(() => [innerWidth, innerHeight, devicePixelRatio])).toEqual([1440, 900, 2])
})

test('empty chat and skill picker use the desktop controls without changing phones', async ({ page, workspaceKey }) => {
  await page.goto(`/${workspaceKey}/agent`)
  await expect(page.getByText('Get started with some examples', { exact: true })).toBeVisible()
  await expect(page.locator('[data-agent-send]')).toBeEnabled()
  await page.getByRole('button', { name: 'Skills', exact: true }).click()
  const picker = page.getByRole('dialog').filter({ has: page.getByRole('option', { name: 'Create skill' }) })
  await expect(picker).toBeVisible()
  await expect(picker).toHaveCSS('min-width', '174px')
  await expect(picker).toHaveCSS('padding', '0px')
  await expect(picker.getByRole('option', { name: 'Create skill' })).toHaveCSS('height', '32px')
  await expect(picker.getByRole('textbox', { name: 'Search skills…' })).not.toBeInViewport()
  await expect(picker.getByText('Select skill', { exact: true })).toBeHidden()
  await page.waitForTimeout(500)
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/agent-skills.png`, animations: 'disabled' })
  await picker.getByRole('option', { name: 'Create skill' }).click()
  await expect(page).toHaveURL(/settings\/skill\/new$/)
})

test('loop list owner is interactive and the editor retains compact daily controls', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const response = await request.post('/api/loops', { headers, data: { name: '[audit] Daily review', level: 'workspace', status: 'draft', instructions: 'Review the workspace.', outputContract: { source: 'person', readOnly: true, outputs: [] }, triggerType: 'schedule', triggerConfig: { interval: 1, unit: 'day', time: '23:00' } } })
  expect(response.status()).toBe(201)
  const loop = await response.json() as { id: string }
  await page.goto(`/${workspaceKey}/loops/all`)
  const owner = page.getByRole('button', { name: 'Change owner', exact: true })
  await expect(owner).toHaveCSS('height', '28px')
  await owner.click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await page.goto(`/${workspaceKey}/loops/new?draftId=${loop.id}`)
  await expect(page.getByRole('textbox', { name: 'Loop name' })).toHaveCSS('font-size', '32px')
  await expect(page.getByRole('button', { name: 'Loop level' })).toBeHidden()
  await expect(page.getByRole('group', { name: 'Weekdays' })).toHaveCount(0)
  await expect(page.getByRole('spinbutton', { name: 'Interval', exact: true })).toHaveCSS('height', '26px')
  await expect(page.getByText('Reply only: the run changes nothing', { exact: true })).toBeVisible()
  await page.waitForTimeout(500)
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/agent-loop-editor.png`, animations: 'disabled' })
})

test('run history has its tabs, overflow menu and title disclosure', async ({ page, request, workspaceKey }) => {
  const response = await request.post('/api/loops', { headers: { 'X-Workspace-Key': workspaceKey }, data: { name: '[audit] Daily review', level: 'workspace', status: 'published', enabled: false, instructions: 'Review the workspace.' } })
  expect(response.status()).toBe(201)
  const loop = await response.json() as { id: string }
  const run = { id: 'audit-run', loopId: loop.id, status: 'completed', trigger: 'manual', triggerLabel: 'Manual run', startedAt: '2026-10-30T10:00:00Z', finishedAt: '2026-10-30T10:00:01Z', output: 'Review complete.' }
  await page.route(`**/api/loops/${loop.id}/runs**`, route => route.fulfill({ json: route.request().url().endsWith('/runs') ? [run] : run }))
  await page.goto(`/${workspaceKey}/loop/${loop.id}/run/${run.id}`)
  await expect(page.getByRole('button', { name: 'Overview', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'This Loop is disabled', exact: true })).toBeDisabled()
  const instructions = page.getByRole('button', { name: 'Instructions', exact: true })
  await expect(instructions).toHaveAttribute('aria-expanded', 'false')
  await instructions.click()
  await expect(instructions).toHaveAttribute('aria-expanded', 'true')
  await page.getByRole('button', { name: 'Loop actions', exact: true }).click()
  await expect(page.getByRole('menuitem', { name: 'Show run history', exact: true })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('menu')).toHaveCount(0)
  await page.waitForTimeout(500)
  if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/agent-loop-run.png`, animations: 'disabled' })
})
