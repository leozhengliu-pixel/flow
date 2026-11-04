import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import { bootstrap, preferences } from './review-findings-fixture'

for (const width of [375, 390]) for (const theme of ['Dark', 'Light'] as const) for (const locale of ['en-US', 'zh-CN']) {
  test(`keeps labels whole beside sub-issue counts ${width} ${theme} ${locale}`, async ({ page, request, workspaceKey }) => {
    await page.setViewportSize({ width, height: width === 375 ? 667 : 844 })
    await preferences(request, workspaceKey, theme, locale)
    const headers = { 'X-Workspace-Key': workspaceKey }
    const data = await bootstrap(request, workspaceKey)
    expect((await request.patch('/api/account/profile', { headers, data: { displayName: '中文', username: 'reviewer' } })).ok()).toBe(true)
    const response = await request.post('/api/labels', { headers, data: { name: 'Bug', color: '#eb5757' } })
    expect(response.status()).toBe(201)
    const label = await response.json()
    for (const title of ['Plain issue', '中文 Infrastructure reliability and regional launch dependencies']) {
      const created = await request.post('/api/issues', { headers, data: { title, teamId: data.teams[0].id, stateId: data.states.find(state => state.type === 'unstarted')!.id, assigneeId: data.viewer.id, labelIds: [label.id] } })
      expect(created.status()).toBe(201)
      const parent = await created.json()
      expect((await request.post('/api/issues', { headers, data: { title: 'Child', teamId: data.teams[0].id, stateId: parent.state.id, parentId: parent.id } })).status()).toBe(201)
      await page.goto(`/${workspaceKey}/my-issues/assigned`)
      const row = page.locator(`[data-issue-row-id="${parent.id}"]`)
      await expect(row.locator('[class*=subIssueProgress]')).toContainText('0/1')
      const pill = row.locator('[class*=badgeSlot] > [class*=badge]').first()
      await expect(pill).toHaveText('Bug')
      expect((await row.locator('.issue-assignee-cluster [data-user-avatar]').last().boundingBox())!.x).toBe(width - 39)
      expect(await pill.evaluate(element => {
        const box = element.getBoundingClientRect()
        for (let parent = element.parentElement; parent && !parent.hasAttribute('data-issue-row-id'); parent = parent.parentElement) {
          if (getComputedStyle(parent).overflowX === 'hidden' && (box.left < parent.getBoundingClientRect().left || box.right > parent.getBoundingClientRect().right)) return false
        }
        return true
      })).toBe(true)
      await row.hover()
      await row.focus()
      await expect(row).toBeFocused()
      if (process.env.FLOW_CI_SHOTS && title === 'Plain issue' && locale === 'en-US') await row.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/plain-row-${width}-${theme}.png` })
    }
  })
}
