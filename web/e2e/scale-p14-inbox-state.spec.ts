import { expect } from '@playwright/test'
import { test } from './workspace-fixture'

for (const surface of [
  { name: 'desktop', viewport: { width: 1440, height: 900 }, mobile: false, locale: 'en-US', theme: 'light' },
  { name: 'phone', viewport: { width: 390, height: 844 }, mobile: true, locale: 'zh-CN', theme: 'dark' },
] as const) {
  test.describe(`P14 recipient inbox ${surface.name}`, () => {
    test.use({ viewport: surface.viewport, isMobile: surface.mobile, hasTouch: surface.mobile, colorScheme: surface.theme })

    test('applies recipient counts and row invalidation without a count GET', async ({ page, request, workspaceKey }, testInfo) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const preferences = await (await request.get('/api/account/settings', { headers })).json() as { language: string; interfaceTheme: string }
      const saved = await request.patch('/api/account/settings', { headers, data: { language: surface.locale, interfaceTheme: surface.theme === 'light' ? 'Light' : 'Dark' } })
      expect(saved.status()).toBe(200)
      try {
        const boot = await (await request.get('/api/bootstrap', { headers })).json() as { viewer: { id: string }; teams: { id: string }[] }
        const issueResponse = await request.post('/api/issues', { headers, data: { title: 'P14 inbox recipient update', teamId: boot.teams[0].id, stateId: 'state_todo' } })
        expect(issueResponse.status()).toBe(201)
        const issue = await issueResponse.json() as { id: string }
        const reminder = await request.post(`/api/issues/${issue.id}/reminders`, { headers, data: { remindAt: new Date(Date.now() + 300).toISOString() } })
        expect(reminder.status()).toBe(201)
        let item: { id: string } | undefined
        await expect.poll(async () => {
          const inbox = await (await request.get('/api/notifications?limit=50', { headers })).json() as { notifications: { id: string }[] }
          item = inbox.notifications[0]
          return inbox.notifications.length
        }, { timeout: 15000 }).toBe(1)
        await page.addInitScript(({ locale, theme }) => {
          localStorage.setItem('flow:locale', locale)
          localStorage.setItem('flow:theme', theme)
        }, surface)
        await page.goto(`/${workspaceKey}/inbox`)
        await expect(page.locator('html')).toHaveAttribute('lang', surface.locale === 'zh-CN' ? 'zh-CN' : 'en')
        const row = page.locator('.flow-inbox-notification-list > [role="listitem"]').first()
        await expect(row).toBeVisible()
        await page.evaluate(async ({ workspaceKey, viewerId }) => {
          const path = '/src/lib/inbox-unread.ts'
          const { inboxUnread, INBOX_STATE_CAPABILITY } = await import(path)
          inboxUnread.configure({ workspaceKey, viewerId, capabilities: [INBOX_STATE_CAPABILITY] })
          inboxUnread.apply(workspaceKey, viewerId, { revision: '1', unreadCount: 1, invalidated: false })
        }, { workspaceKey, viewerId: boot.viewer.id })
        let unreadGets = 0
        page.on('request', req => {
          const url = new URL(req.url())
          if (url.pathname === '/api/notifications/state' || url.pathname === '/api/notifications' && url.searchParams.get('limit') === '1') unreadGets++
        })
        const update = await request.patch(`/api/notifications/${item!.id}`, { headers, data: { read: true } })
        expect(update.status()).toBe(200)
        await page.evaluate(async ({ workspaceKey, viewerId, id }) => {
          const path = '/src/lib/inbox-unread.ts'
          const { inboxUnread } = await import(path)
          // P13 forwards P12's recipient frame through this public interface.
          inboxUnread.receive(workspaceKey, viewerId, { type: 'notification.state', aggregateId: viewerId, payload: { revision: '2', unreadCount: 0, changedIds: [id], invalidated: false } })
        }, { workspaceKey, viewerId: boot.viewer.id, id: item!.id })
        await expect(page.locator('.flow-inbox-row').first()).toHaveAttribute('data-read', 'true')
        await expect.poll(async () => page.evaluate(async () => {
          const path = '/src/lib/inbox-unread.ts'
          return (await import(path)).inboxUnread.state.count
        })).toBe(0)
        expect(unreadGets).toBe(0)
        await page.evaluate(async ({ workspaceKey, viewerId }) => {
          const path = '/src/lib/inbox-unread.ts'
          const { inboxUnread } = await import(path)
          inboxUnread.configure({ workspaceKey, viewerId, capabilities: [] })
        }, { workspaceKey, viewerId: boot.viewer.id })
        await expect.poll(() => unreadGets).toBeGreaterThan(0)
        await expect(page.locator('.flow-inbox-row').first()).toHaveAttribute('data-read', 'true')
        const overflows = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)
        expect(overflows).toBe(false)
        if (process.env.FLOW_CI_SHOTS) await page.screenshot({ path: `${process.env.FLOW_CI_SHOTS}/p14-${surface.name}-${testInfo.project.name}.png`, fullPage: true, animations: 'disabled' })
      } finally {
        expect((await request.patch('/api/account/settings', { headers, data: { language: preferences.language, interfaceTheme: preferences.interfaceTheme } })).status()).toBe(200)
      }
    })
  })
}
