import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import { createServer } from 'vite'
import { agentPanelSeedPlugin } from './agent-panel-seed-plugin'

const api = process.env.FLOW_MOBILE_API ?? 'http://127.0.0.1:4180'

type Chat = { id: string; title: string }

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test.describe(`mobile review agent panel ${viewport.width}px`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark' })

    test('linked issue, pressed toggle, uniform surface and consistent tool gaps', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const bootstrap = await (await request.get(`${api}/api/bootstrap`, { headers })).json()
      const created = await request.post(`${api}/api/issues`, { headers, data: { title: '[mobile] Agent panel fixture issue', teamId: bootstrap.teams[0].id, stateId: 'state_progress' } })
      expect(created.status()).toBe(201)
      const issue = await created.json() as { id: string; identifier: string }
      const server = await createServer({ plugins: [agentPanelSeedPlugin(api, workspaceKey)], server: { host: '127.0.0.1', port: 0, proxy: { '/api': { target: api, ws: true } } }, logLevel: 'silent' })
      try {
        await server.listen()
        const address = server.httpServer!.address() as { port: number }
        await page.goto(`http://127.0.0.1:${address.port}/${workspaceKey}/review/add-exported-whisper2-function/changes?showAgent=true&agentSessionId=mobile-agent-agent-panel`)
        const panel = page.locator('.review-agent-panel')
        await expect(panel).toBeVisible()
        const toggle = page.getByRole('button', { name: 'Hide agent', exact: true })
        await expect(toggle).toHaveAttribute('aria-pressed', 'true')
        await expect(toggle.locator('.review-agent-toggle-filled')).toBeVisible()
        expect((await toggle.boundingBox())?.width).toBe(32)
        expect(await toggle.evaluate(node => getComputedStyle(node).backgroundColor)).toBe('lch(18.634 1.075 272)')
        const link = panel.locator('.coding-session-prompt a')
        await expect(link).toHaveText(issue.identifier)
        await expect(link).toHaveAttribute('href', new RegExp(`/issue/${issue.identifier}/`))
        expect(await panel.locator('.coding-agent-panel-surface').evaluate(node => getComputedStyle(node).backgroundColor)).toBe(await panel.locator('.coding-agent-panel-header').evaluate(node => getComputedStyle(node).backgroundColor))
        await panel.locator('.cs-group-header').first().click()
        const gaps = await panel.locator('.cs-action-header').evaluateAll(rows => rows.map(row => {
          const label = row.querySelector('.cs-action-label')!.getBoundingClientRect()
          return row.querySelector('.cs-action-detail')!.getBoundingClientRect().left - label.right
        }))
        for (const gap of gaps) expect(Math.abs(gap - 6)).toBeLessThan(.1)
        expect(await panel.locator('.cs-action-label').first().evaluate(node => getComputedStyle(node).fontFamily)).toContain('Inter Variable')
        expect(await panel.locator('.cs-action-detail code').first().evaluate(node => getComputedStyle(node).fontFamily)).toContain('ui-monospace')
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
        await toggle.click()
        await expect(panel).toBeHidden()
        await expect(page.getByRole('button', { name: 'Show agent', exact: true })).toHaveAttribute('aria-pressed', 'false')
      } finally { await server.close() }
    })
  })
}

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
  test.describe(`mobile agent chat switcher ${viewport.width}px`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark' })

    test('history has calendar groups, filters, scrolls and opens a chat', async ({ page, workspaceKey }) => {
      const now = new Date('2026-10-30T12:00:00')
      await page.clock.setFixedTime(now)
      const sessions = [1, 3, 10, 17, 17.1, 24, 24.1, 90].map((days, index) => ({
        id: `switcher-${index}`, slugId: `switcher-${index}`, userId: 'usr_local', title: titles[index], favorite: false, location: 'page', issueIds: [], skillIds: [], messages: [],
        createdAt: new Date(+now - days * 86400000).toISOString(), updatedAt: new Date(+now - days * 86400000).toISOString(),
      }))
      await page.route('**/api/**', async route => {
        if (route.request().method() !== 'GET' || route.request().headers().accept?.includes('text/event-stream')) return route.continue()
        const response = await route.fetch()
        if (!response.headers()['content-type']?.includes('application/json')) return route.fulfill({ response })
        const body = await response.json()
        if (body && !Array.isArray(body) && 'agentSessions' in body) body.agentSessions = sessions
        if (new URL(route.request().url()).pathname === '/api/agent/sessions') return route.fulfill({ response, json: sessions })
        if (new URL(route.request().url()).pathname === '/api/agent/sessions/switcher-3') return route.fulfill({ response, status: 200, json: sessions[3] })
        await route.fulfill({ response, json: body })
      })
      await page.goto(`/${workspaceKey}/agent`)
      const trigger = page.getByRole('button', { name: 'Switch agent chat' })
      await trigger.click()
      const popover = page.getByTestId('agent-chat-switcher-popover')
      await expect.poll(async () => (await popover.boundingBox())?.width).toBe(viewport.width - 32)
      const box = await popover.boundingBox()
      expect(box?.x).toBe(16)
      expect(box?.width).toBe(viewport.width - 32)
      expect(box?.height).toBe(450)
      expect(Math.abs((box?.y ?? 0) - (viewport.width === 390 ? 110 : 87))).toBeLessThanOrEqual(1)
      for (const name of ['Yesterday', 'This week', 'Last week', '2 weeks ago', '3 weeks ago', 'Older']) await expect(popover.getByRole('group', { name, exact: true })).toBeAttached()
      expect((await popover.getByRole('option').first().boundingBox())?.height).toBe(46)
      const time = popover.getByRole('option').first().locator('time')
      expect(Math.abs(((await time.boundingBox())?.x ?? 0) + ((await time.boundingBox())?.width ?? 0) - (viewport.width - 33.5))).toBeLessThanOrEqual(1)
      await expect(popover.locator('[data-history-section="weeks-3"]').first().locator('time')).toHaveText('3w')
      await expect(popover).toHaveAttribute('data-phone-popover')
      await expect(popover).toHaveCSS('border-top-style', 'solid')
      await expect(popover).toHaveCSS('border-top-color', 'lch(25.68 1.93 272)')
      await expect(popover.getByRole('option').first()).toHaveAttribute('aria-selected', 'false')
      expect(await popover.getByRole('listbox').evaluate(node => node.scrollHeight > node.clientHeight)).toBe(true)
      expect(await popover.getByTestId('agent-chat-switcher-divider').count()).toBe(5)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
      if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/chat-switcher-${viewport.width}.png`, scale: 'css', animations: 'disabled' })
      const input = popover.getByPlaceholder('Chat history')
      await input.fill('QA Pill')
      await expect(popover.getByRole('option')).toHaveCount(1)
      await input.fill('no matching chat')
      await expect(popover.getByText('No history')).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(popover).toBeHidden()
      await trigger.click()
      await expect(popover.getByPlaceholder('Chat history')).toHaveValue('')
      await page.mouse.click(viewport.width - 8, 64)
      await expect(popover).toBeHidden()
      await trigger.click()
      await popover.getByPlaceholder('Chat history').fill('QA Pill')
      await page.keyboard.press('Enter')
      await expect(page).toHaveURL(new RegExp(`/${workspaceKey}/agent/switcher-3$`))
    })

    test('phone display options keep the measured layout pill borders without shadows', async ({ page, request, workspaceKey }) => {
      const response = await request.get('/api/bootstrap', { headers: { 'X-Workspace-Key': workspaceKey } })
      const { teams } = await response.json() as { teams: { key: string }[] }
      await page.goto(`/${workspaceKey}/team/${teams[0].key}/all`)
      await page.getByRole('button', { name: 'Display options', exact: true }).click()
      const dialog = page.getByRole('dialog', { name: 'Display options' })
      await expect(dialog).toHaveAttribute('data-phone-popover')
      for (const name of ['List', 'Board']) {
        const tab = dialog.getByRole('tab', { name, exact: true })
        await expect(tab).toHaveCSS('border-top-style', 'solid')
        await expect(tab).toHaveCSS('border-top-color', 'rgba(0, 0, 0, 0)')
        await expect(tab).toHaveCSS('box-shadow', 'none')
        await expect(tab).toHaveCSS('height', '28px')
      }
    })
  })
}

for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test.describe(`mobile agent home ${viewport.width}px`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark' })

    test('phone Agent home shows the new-chat header, composer and recent chats', async ({ page, request, workspaceKey }) => {
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
      await expect.poll(() => send.evaluate(node => getComputedStyle(node).backgroundColor)).toBe('lch(47.918 59.303 288.421)')

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


for (const viewport of [{ width: 390, height: 844 }, { width: 375, height: 667 }]) {
  test.describe(`mobile agent conversation ${viewport.width}px`, () => {
    test.use({ viewport, deviceScaleFactor: 3, isMobile: true, hasTouch: true, colorScheme: 'dark' })

    test('phone conversation shows the prompt, expanded steps, linked team card and reply composer', async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const prompt = 'Create a team called Invite Test and add 3 people to it: qa-invite-1@example.com, qa-invite-2@example.com, qa-invite-3@example.com (they are not members of this workspace yet).'
      const created = await request.post(`${api}/api/agent/sessions`, { headers, data: { message: prompt } })
      expect(created.status()).toBe(201)
      const chat = await created.json() as Chat & { slugId: string }

      await page.goto(`/${workspaceKey}/agent/${chat.slugId}`)

      const header = page.locator('main > header')
      expect((await header.boundingBox())?.height).toBe(44)
      expect((await page.locator('main > header [data-sidebar-trigger]').boundingBox())?.x).toBe(8)
      const favorite = page.locator('main > header button[aria-label="Add to favorites"]')
      expect((await favorite.boundingBox())?.x).toBeCloseTo(323.69, 1)
      expect((await favorite.boundingBox())?.width).toBe(32)
      expect((await page.locator('main > header button[aria-label="Chat options"]').boundingBox())?.x).toBeCloseTo(355.69, 1)
      await expect(page.locator('main > header button[aria-label="Move to toolbar"]')).toBeHidden()

      const scroll = page.locator('main [role="group"][aria-label="Agent conversation"]')
      expect((await scroll.boundingBox())?.y).toBe(44)
      expect((await scroll.boundingBox())?.height).toBe(viewport.width === 390 ? 652 : 475)

      const bubble = page.locator('main article:first-of-type > div:first-child')
      expect((await bubble.boundingBox())?.width).toBe(263)
      expect((await bubble.boundingBox())?.height).toBe(160)
      expect((await page.locator('main article:first-of-type [role="document"]').boundingBox())?.height).toBe(144)
      expect(await page.locator('main article:first-of-type a').count()).toBe(0)

      await page.locator('main details > summary').click()
      await page.waitForTimeout(300)
      const scrollTop = await scroll.evaluate(node => node.scrollTop)
      if (viewport.width === 390) expect(scrollTop).toBe(0)
      else expect(scrollTop).toBeGreaterThan(0)
      const toggle = page.locator('main details > summary')
      expect((await toggle.boundingBox())?.x).toBe(14)
      expect((await toggle.boundingBox())?.height).toBe(16)
      await expect(page.locator('main details > div > div:nth-child(1) span').first()).toHaveText('Created team')
      await expect(page.locator('main details > div > div:nth-child(2) span').first()).toHaveText('Searched documentation')
      const latest = page.locator('main [aria-label="Latest — Scroll to latest"]')
      if (await latest.count()) await latest.click()

      const response = page.locator('main article:nth-of-type(2) [role="document"][aria-label="AI message"]')
      expect((await response.boundingBox())?.x).toBe(12)
      expect((await response.boundingBox())?.width).toBe(viewport.width === 390 ? 356 : 341)
      expect((await response.boundingBox())?.height).toBe(208)
      expect((await page.locator('main article:nth-of-type(2) code').boundingBox())?.height).toBe(65)

      const card = page.locator('main section[aria-label="Created team"] > div:nth-child(2)')
      expect((await card.boundingBox())?.x).toBe(12)
      expect((await card.boundingBox())?.width).toBe(viewport.width - 24)
      expect(Math.abs(((await card.boundingBox())?.height ?? 0) - 37)).toBeLessThanOrEqual(1)
      await expect(page.locator('main section[aria-label="Created team"] a span').first()).toHaveText('Invite Test')

      const actions = page.locator('main [data-agent-reply-actions]')
      expect((await actions.boundingBox())?.width).toBe(viewport.width - 24)
      expect((await actions.boundingBox())?.height).toBe(28)
      expect((await page.locator('main article:nth-of-type(2) button[aria-label="This was helpful"]').boundingBox())?.width).toBe(28)

      const composer = page.locator('main [data-agent-composer]')
      expect((await composer.boundingBox())?.x).toBe(12)
      expect((await composer.boundingBox())?.y).toBe(viewport.width === 390 ? 700 : 523)
      expect((await composer.boundingBox())?.height).toBe(132)
      const send = page.locator('main [data-agent-composer] [data-agent-send]')
      expect((await send.boundingBox())?.width).toBe(24)
      expect((await send.boundingBox())?.height).toBe(24)
      expect(await send.evaluate(node => getComputedStyle(node).backgroundColor)).toBe('lch(17.574 1.589 272)')
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)

      await page.locator('main > header button[aria-label="Chat options"]').click()
      const chatOptions = page.locator('.agent-chat-options-phone-menu')
      await expect(chatOptions).toBeVisible()
      await page.waitForTimeout(300)
      expect((await chatOptions.boundingBox())?.x).toBe(16)
      expect((await chatOptions.boundingBox())?.width).toBe(viewport.width - 32)
      expect(Math.abs(((await chatOptions.boundingBox())?.height ?? 0) - 208)).toBeLessThanOrEqual(1)
      await expect(chatOptions.locator('input')).toHaveAttribute('placeholder', 'Filter…')
      await expect(chatOptions.getByRole('option')).toHaveText(['Rename…', 'Copy as markdown', 'Delete'])
      expect(['0.5px', '1px']).toContain(await chatOptions.locator('.agent-chat-options-divider').evaluate(node => getComputedStyle(node).height))

      if (process.env.FLOW_MOBILE_SHOTS) await page.screenshot({ path: `${process.env.FLOW_MOBILE_SHOTS}/agent-conversation-${viewport.width}.png`, scale: 'css', animations: 'disabled' })
    })
  })
}
