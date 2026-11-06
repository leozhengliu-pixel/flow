import { expect, type Page } from '@playwright/test'
import { test } from './workspace-fixture'
import type { Project } from '../src/types/flow'

test.use({ timezoneId: 'UTC', viewport: { width: 1440, height: 900 } })

// The shots land in FLOW_CHAT_DIALOG_SHOTS when set (reviewing only; the assertions run either way).
const shots = process.env.FLOW_CHAT_DIALOG_SHOTS

async function mockChat(page: Page, locale: string, theme: string) {
  await page.addInitScript(({ locale, theme }) => {
    localStorage.setItem('flow.theme', JSON.stringify({ interfaceTheme: theme }))
    localStorage.setItem('flow:locale', locale)
  }, { locale, theme })
  await page.route('**/api/bootstrap', async route => {
    const response = await route.fetch()
    const data = await response.json()
    data.chatPlatform = 'feishu'
    data.userSettings[data.viewer.id] = { ...data.userSettings[data.viewer.id], language: locale, interfaceTheme: theme, timezone: 'UTC' }
    await route.fulfill({ response, json: data })
  })
  await page.route('**/api/integrations/feishu/chat', async route => {
    const bootstrap = await (await page.request.get('/api/bootstrap', { headers: { 'X-Workspace-Key': new URL(page.url()).pathname.split('/')[1] } })).json()
    await route.fulfill({ json: {
      connection: { id: 'chat-review', provider: 'feishu', name: 'Feishu', status: 'connected', config: {}, scopes: [], channels: [] },
      identity: null, bindings: [], bindableTeamIds: bootstrap.teams.map((team: { id: string }) => team.id), credentialsConfigured: true, webhookURL: '', canManage: true,
      permissions: { workspace: { canManage: true }, tenant: { status: 'authorized' }, teams: { manageableTeamIds: bootstrap.teams.map((team: { id: string }) => team.id), privateTeamIds: [] } },
    } })
  })
  await page.route('**/api/integrations/feishu/chat/chats*', route => route.fulfill({ json: { items: [{ id: 'oc_room', name: 'Product launch', external: false }, { id: 'oc_ops', name: 'Operations', external: false }], hasMore: false } }))
}

for (const theme of ['light', 'dark']) {
  for (const locale of ['en-US', 'zh-CN']) {
    test(`project chat group dialog uses the shared controls ${theme} ${locale}`, async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const projectResponse = await request.post('/api/projects', { headers, data: { name: 'Chat dialog project' } })
      expect(projectResponse.status()).toBe(201)
      const project = await projectResponse.json() as Project
      await mockChat(page, locale, theme)
      const errors: string[] = []
      page.on('pageerror', error => errors.push(error.message))
      await page.goto(`/${workspaceKey}/project/${project.slugId}/overview`)
      await page.locator('.project-details-sidebar__property-trigger.is-empty', { hasText: /Feishu|飞书/ }).click()
      const dialog = page.locator('.project-chat-group-dialog')
      await expect(dialog).toBeVisible()
      await dialog.locator('button.feature-button', { hasText: /Connect Feishu group|连接 ?飞书 ?群/ }).click()
      const form = dialog.locator('form.chat-binding-editor')
      await expect(form).toBeVisible()
      await expect(page.locator('html')).toHaveAttribute('data-locale', locale)
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)

      // Selects are the shared settings select; toggles are the shared switch, not bare checkboxes.
      const selects = form.locator('button.settings-select')
      expect(await selects.count()).toBeGreaterThanOrEqual(3)
      const toggles = form.locator('input[data-control="toggle"]')
      expect(await toggles.count()).toBeGreaterThanOrEqual(9)

      if (shots) {
        await dialog.scrollIntoViewIfNeeded()
        await page.screenshot({ path: `${shots}/dialog-${theme}-${locale}.png` })
      }
      const geometry = await dialog.evaluate(root => {
        const box = root.getBoundingClientRect()
        const outside = [...root.querySelectorAll<HTMLElement | SVGElement>('*')].filter(element => {
          const rect = element.getBoundingClientRect()
          return rect.width > 0 && (rect.left < box.left - 0.5 || rect.right > box.right + 0.5)
        }).map(element => `${element.tagName}.${element.getAttribute('class') ?? ''}`)
        const chevrons = [...root.querySelectorAll('svg')].map(svg => svg.getBoundingClientRect()).filter(rect => rect.width > 24 || rect.height > 24)
        const select = root.querySelector('button.settings-select')!.getBoundingClientRect()
        const toggle = root.querySelector('input[data-control="toggle"]')!.getBoundingClientRect()
        const row = root.querySelector('.feature-row')!
        const aside = row.querySelector('aside')!.getBoundingClientRect()
        const copy = row.querySelector('div')!.getBoundingClientRect()
        return { outside, bigIcons: chevrons.length, selectHeight: select.height, selectWidth: select.width, toggleWidth: toggle.width, toggleHeight: toggle.height, asideLeft: aside.left, copyRight: copy.right, copyLeft: copy.left, dialogLeft: box.left, scrolls: root.scrollWidth > root.clientWidth + 1 }
      })
      expect(geometry.outside).toEqual([])
      expect(geometry.bigIcons).toBe(0)
      expect(geometry.scrolls).toBe(false)
      expect(geometry.selectHeight).toBeLessThanOrEqual(36)
      expect(geometry.selectWidth).toBeGreaterThan(80)
      expect([geometry.toggleWidth, geometry.toggleHeight]).toEqual([30, 20])
      // Label and description sit left of the control column.
      expect(geometry.copyLeft).toBeLessThan(geometry.asideLeft)
      expect(geometry.copyRight).toBeLessThanOrEqual(geometry.asideLeft + 0.5)

      // A toggle flips like a switch.
      const first = toggles.first()
      const before = await first.isChecked()
      await first.click()
      expect(await first.isChecked()).toBe(!before)
      // A select opens its shared menu.
      await selects.nth(1).click()
      await expect(page.getByRole('listbox')).toBeVisible()
      await page.keyboard.press('Escape')
      await expect(page.getByRole('listbox')).toBeHidden()

      // The form scrolls inside the dialog, which stays on screen with its actions reachable.
      const bottom = await dialog.evaluate(root => {
        const body = root.querySelector<HTMLElement>('.chat-group-dialog__body')!
        const scrollable = body.scrollHeight > body.clientHeight
        body.scrollTop = body.scrollHeight
        const box = root.getBoundingClientRect()
        const save = [...root.querySelectorAll('button')].find(button => /Save group binding|保存群绑定/.test(button.textContent ?? ''))!.getBoundingClientRect()
        return { scrollable, top: box.top, bottom: box.bottom, viewport: innerHeight, saveBottom: save.bottom }
      })
      expect(bottom.scrollable).toBe(true)
      expect(bottom.top).toBeGreaterThanOrEqual(0)
      expect(bottom.bottom).toBeLessThanOrEqual(bottom.viewport)
      expect(bottom.saveBottom).toBeLessThanOrEqual(bottom.bottom)
      await page.keyboard.press('Escape')
      await expect(dialog).toBeHidden()
      expect(errors).toEqual([])
    })
  }
}
