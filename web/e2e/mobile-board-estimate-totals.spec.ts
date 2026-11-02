import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

for (const width of [375, 390, 1440]) {
  test.describe(`board estimate totals at ${width}px`, () => {
    test.use({ viewport: { width, height: 900 }, deviceScaleFactor: width < 800 ? 3 : 2, isMobile: width < 800, hasTouch: width < 800 })
    test('displays points for estimate teams and plain issue counts when estimates are off', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const data = await (await request.get('/api/bootstrap', { headers })).json()
      const team = data.teams[0]
      const state = data.states.find((item: { type: string }) => item.type === 'unstarted')
      expect((await request.patch(`/api/teams/${team.id}/settings`, { headers, data: { estimateType: 'fibonacci', estimateCountUnestimated: true } })).ok()).toBe(true)
      for (const [index, estimate] of [5, 3, undefined].entries()) expect((await request.post('/api/issues', { headers, data: { title: `Estimate fixture ${index}`, teamId: team.id, stateId: state.id, estimate } })).status()).toBe(201)
      await page.addInitScript(({ workspaceKey, teamId }) => localStorage.setItem(`${workspaceKey}:issue-explorer:team:${teamId}:all:display`, JSON.stringify({ layout: 'board', grouping: 'status', properties: ['estimate'], showEmptyGroups: false })), { workspaceKey, teamId: team.id })
      await page.goto(`/${workspaceKey}/team/${team.key}/all`)
      const total = page.locator('[data-board-column-count]').first()
      await expect(total).toHaveText('9', { useInnerText: true })
      await expect(total).toHaveAccessibleName('3 issues, 9 estimate points')
      await expect(total.locator('svg')).toBeVisible()
      expect((await request.patch(`/api/teams/${team.id}/settings`, { headers, data: { estimateType: 'notUsed' } })).ok()).toBe(true)
      await page.reload()
      await expect(total).toHaveText('3', { useInnerText: true })
      await expect(total.locator('svg')).toHaveCount(0)
    })
  })
}
