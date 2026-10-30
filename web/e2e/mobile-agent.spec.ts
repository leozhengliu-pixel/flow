import { expect, test } from '@playwright/test'

const api = 'http://127.0.0.1:4180'

type Chat = { id: string; title: string }

const titles = [
  '[mobile] Picker Test 事项状态与点数',
  '[mobile] Create Invite Test team and add people',
  '[mobile] Describe facebook/react repo with DeepWiki',
  '[mobile] QA Pill Measure Options',
  '[mobile] Choose Next QA Action',
  '[mobile] Formatting Test with FLO-1 and Compare',
  '[mobile] Show Flow-App Team Resources',
  '[mobile] List Flow-app Team Issues and Resources',
]

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test.describe(`mobile agent home ${viewport.width}px`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark' })

    test('phone Agent home shows the new-chat header, composer and recent chats', async ({ page, request }, testInfo) => {
      const workspaceKey = `mobile-agent-home-${viewport.width}-${testInfo.project.name}-${testInfo.retry}`
      expect((await request.post(`${api}/api/workspaces`, {
        data: { name: 'Mobile agent home parity', urlKey: workspaceKey, region: 'us', tutorial: false },
      })).status()).toBe(201)
      const headers = { 'X-Workspace-Key': workspaceKey }

      const chats: Chat[] = []
      for (let index = 0; index < titles.length; index++) {
        const created = await request.post(`${api}/api/agent/sessions`, {
          headers,
          data: { message: `[mobile] chat ${index + 1}` },
        })
        expect(created.status()).toBe(201)
        chats.push(await created.json() as Chat)
      }
      // Newest first: the reference app lists the most recently updated chat at the top.
      for (let index = chats.length - 1; index >= 0; index--) {
        expect((await request.patch(`${api}/api/agent/sessions/${chats[index].id}`, { headers, data: { title: titles[index] } })).ok()).toBe(true)
      }
      for (const chat of chats) expect((await request.post(`${api}/api/agent/sessions/${chat.id}/read`, { headers })).ok()).toBe(true)
      // A later message makes the second chat unread, then the first title edit puts the CJK chat back on top.
      expect((await request.post(`${api}/api/agent/sessions/${chats[1].id}/messages`, { headers, data: { message: '[mobile] unread follow up' } })).ok()).toBe(true)
      const firstTitle = '[mobile] 列出 Picker Test 事项状态与点数'
      expect((await request.patch(`${api}/api/agent/sessions/${chats[0].id}`, { headers, data: { title: firstTitle } })).ok()).toBe(true)

      await page.goto(`/${workspaceKey}/agent`)

      const header = page.locator('main > header')
      expect((await header.boundingBox())?.height).toBe(44)
      expect(await header.evaluate(node => getComputedStyle(node).gap)).toBe('12px')
      expect(await header.evaluate(node => getComputedStyle(node).backgroundColor)).toBe('lch(5.52 0.4 272)')
      const menu = page.locator('main > header [data-sidebar-trigger]')
      expect((await menu.boundingBox())?.x).toBe(8)
      expect((await menu.boundingBox())?.width).toBe(32)
      const switcher = page.locator('main > header [data-agent-chat-switcher]')
      expect((await switcher.boundingBox())?.x).toBe(44)
      expect((await switcher.boundingBox())?.height).toBe(32)
      expect(await page.locator('main > header [data-agent-chat-switcher] h2').textContent()).toBe('New chat')

      const composer = page.locator('main [data-agent-composer]')
      expect((await composer.boundingBox())?.x).toBe(24)
      expect((await composer.boundingBox())?.y).toBe(288)
      expect((await composer.boundingBox())?.width).toBe(viewport.width - 48)
      expect((await composer.boundingBox())?.height).toBe(108)
      expect((await page.locator('main [data-agent-composer] [contenteditable="true"]').boundingBox())?.y).toBe(302)
      expect((await page.locator('main [data-agent-composer] [data-agent-skills]').boundingBox())?.height).toBe(28)
      const attach = page.locator('main [data-agent-composer] [data-agent-attach]')
      expect((await attach.boundingBox())?.x).toBe(viewport.width - 96)
      expect((await attach.boundingBox())?.width).toBe(28)
      const send = page.locator('main [data-agent-composer] [data-agent-send]')
      expect((await send.boundingBox())?.width).toBe(24)
      expect(await send.evaluate(node => getComputedStyle(node).backgroundColor)).toBe('lch(47.918 59.303 288.421)')

      const list = page.locator('main nav')
      expect((await list.boundingBox())?.y).toBe(408)
      expect((await list.boundingBox())?.width).toBe(viewport.width - 48)
      expect((await list.boundingBox())?.height).toBe(viewport.width === 390 ? 262 : 235)
      const rows = page.locator('main nav [data-agent-chat-row]')
      expect((await rows.nth(0).boundingBox())?.height).toBe(34.5)
      expect((await rows.nth(1).boundingBox())?.y).toBe(443.5)
      expect((await rows.nth(1).boundingBox())?.height).toBe(31.5)
      expect((await rows.nth(2).boundingBox())?.y).toBe(476)
      expect(await rows.nth(0).locator('[data-agent-chat-title]').textContent()).toBe(firstTitle)
      expect((await rows.nth(1).locator('[data-agent-chat-title]').boundingBox())?.height).toBe(15.5)
      expect(await rows.nth(1).locator('[data-agent-chat-title]').evaluate(node => getComputedStyle(node).color)).toBe('lch(90.451 1.2 272)')
      const dot = page.locator('main nav [data-agent-unread-dot]')
      expect((await dot.boundingBox())?.width).toBe(8)
      expect((await dot.boundingBox())?.y).toBe(455.25)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)

      if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/agent-home-${viewport.width}.png`, scale: 'css', animations: 'disabled' })
    })
  })
}
