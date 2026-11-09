import { expect, type Page } from '@playwright/test'
import { test } from './workspace-fixture'
import type { Project } from '../src/types/flow'

// The panel's width is its widest label plus fixed gaps, so it needs the real device scale and the subpixel text positioning of a real Mac
// browser (the flags desktop-settings-panel-offset.spec.ts uses); an emulated scale factor also lays a 0.5px border out as 1px.
test.use({ timezoneId: 'UTC', viewport: { width: 1440, height: 900 }, launchOptions: { args: ['--force-device-scale-factor=2', '--font-hinting=none', '--font-render-hinting=none', '--enable-font-subpixel-positioning'] } })

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
    test(`project chat group dialog has the reference window's structure ${theme} ${locale}`, async ({ page, request, workspaceKey }) => {
      const headers = { 'X-Workspace-Key': workspaceKey }
      const projectResponse = await request.post('/api/projects', { headers, data: { name: 'Compare Test' } })
      expect(projectResponse.status()).toBe(201)
      const project = await projectResponse.json() as Project
      await mockChat(page, locale, theme)
      const errors: string[] = []
      page.on('pageerror', error => errors.push(error.message))
      await page.goto(`/${workspaceKey}/project/${project.slugId}/overview`)
      await page.locator('.project-details-sidebar__property-trigger.is-empty', { hasText: /Feishu|飞书/ }).click()
      const dialog = page.locator('.project-chat-group-dialog')
      await expect(dialog).toBeVisible()
      const form = dialog.locator('form.chat-binding-editor')
      await expect(form).toBeVisible()
      await expect(page.locator('html')).toHaveAttribute('data-locale', locale)
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      await page.evaluate(() => document.fonts.ready)
      await page.waitForTimeout(400)
      if (shots) await page.screenshot({ path: `${shots}/dialog-${theme}-${locale}.png` })

      const measured = await dialog.evaluate(root => {
        const round = (value: number) => Math.round(value * 100) / 100
        const box = root.getBoundingClientRect()
        const rel = (el: Element) => { const r = el.getBoundingClientRect(); return { x: round(r.left - box.left), y: round(r.top - box.top), w: round(r.width), h: round(r.height), right: round(box.right - r.right), bottom: round(box.bottom - r.bottom) } }
        const css = (el: Element, ...props: string[]) => Object.fromEntries(props.map(prop => [prop, getComputedStyle(el).getPropertyValue(prop)]))
        const q = (selector: string) => root.querySelector(selector)!
        const header = q('.chat-group-dialog__header')
        const title = q('.chat-group-dialog__header h2')
        const icons = [...title.querySelectorAll(':scope > img, :scope > svg')].map(rel)
        const checkboxes = [...root.querySelectorAll('.chat-group-dialog__options [role=checkbox]')]
        const labels = [...root.querySelectorAll('.chat-group-dialog__options label')]
        const toggle = q('.chat-dialog-head input[data-control="toggle"]')
        return {
          root: { w: box.width, ...css(root, 'border-top-width', 'border-top-left-radius', 'padding-top') },
          header: { ...rel(header), ...css(title, 'font-size', 'font-weight', 'line-height') },
          icons,
          close: rel(q('.chat-group-dialog__close')),
          description: { ...rel(q('.chat-group-dialog__description')), ...css(q('.chat-group-dialog__description'), 'font-size', 'font-weight', 'line-height') },
          divider: { ...rel(q('.chat-group-dialog__body')), ...css(q('.chat-group-dialog__body'), 'border-top-width') },
          head: { ...rel(q('.chat-dialog-head')), title: css(q('.chat-dialog-head strong'), 'font-size', 'font-weight', 'line-height'), description: css(q('.chat-dialog-head span'), 'font-size', 'font-weight', 'line-height') },
          toggle: rel(toggle),
          checkboxes: checkboxes.map(rel),
          pitch: labels.slice(1).map((label, index) => round(label.getBoundingClientRect().top - labels[index].getBoundingClientRect().top)),
          label: css(labels[0], 'font-size', 'font-weight', 'line-height'),
          checkbox: css(checkboxes[0], 'border-top-left-radius', 'border-top-width'),
          group: rel(root.querySelector('.chat-dialog-rows .feature-row')!),
          height: box.height,
        }
      })
      // The reference's project Slack window, measured from a 2x capture (dark): a 700px dialog with a .5px border, 13px radius and 20px
      // padding; a 26px header row with an 18px app mark, an 18px/500 title, a 16px subject icon and a 28px close button 20px from the
      // edge; a 13px description 17px under it; a 1px divider inset 20px, 17px below that; a switch section with a 13px/500 title and a
      // 12px description over a 30x20 switch; then rows on a 32px pitch, 12px labels, 14px checkboxes centred in the switch's 30px column.
      expect(measured.root).toEqual({ w: 700, 'border-top-width': '0.5px', 'border-top-left-radius': '13px', 'padding-top': '20px' })
      expect(measured.header).toMatchObject({ x: 0.5, y: 20.5, w: 699, h: 26, 'font-size': '18px', 'font-weight': '500', 'line-height': '26px' })
      expect(measured.icons.map(({ x, y, w, h }) => ({ ...(x === 20.5 ? { x } : {}), y, w, h }))).toEqual([{ x: 20.5, y: 24.5, w: 18, h: 18 }, { y: 25.5, w: 16, h: 16 }])
      expect(measured.close).toMatchObject({ y: 19.5, w: 28, h: 28, right: 20.5 })
      expect(measured.description).toMatchObject({ y: 63.5, h: 20, 'font-size': '13px', 'font-weight': '400', 'line-height': '20px' })
      expect(measured.divider).toMatchObject({ x: 20.5, y: 100.5, w: 659, right: 20.5, 'border-top-width': '1px' })
      expect(measured.head).toMatchObject({ x: 20.5, y: 101.5, w: 659, title: { 'font-size': '13px', 'font-weight': '500', 'line-height': '20px' }, description: { 'font-size': '12px', 'font-weight': '400', 'line-height': '20px' } })
      expect(measured.toggle).toMatchObject({ y: 128, w: 30, h: 20, right: 20.5 })
      expect(measured.group).toMatchObject({ x: 20.5, y: 170.5, w: 659, h: 32 })
      expect(measured.checkboxes.map(({ x, w, h, right }) => ({ x, w, h, right }))).toEqual(Array.from({ length: 6 }, () => ({ x: 657.5, w: 14, h: 14, right: 28.5 })))
      expect(measured.checkboxes.map(box => box.y)).toEqual([211.5, 243.5, 275.5, 307.5, 339.5, 371.5])
      expect(measured.pitch).toEqual([32, 32, 32, 32, 32])
      expect(measured.label).toEqual({ 'font-size': '12px', 'font-weight': '400', 'line-height': '20px' })
      expect(measured.checkbox).toEqual({ 'border-top-left-radius': '3px', 'border-top-width': '1px' })
      expect(measured.height).toBe(592)
      expect(errors).toEqual([])
      await page.unrouteAll({ behavior: 'ignoreErrors' })
    })
  }
}

test('the notification panel keeps the chat row and the schedule row on the reference measurements', async ({ page, request, workspaceKey }) => {
  const headers = { 'X-Workspace-Key': workspaceKey }
  const project = await (await request.post('/api/projects', { headers, data: { name: 'Panel project' } })).json() as Project
  await mockChat(page, 'en-US', 'light')
  await page.goto(`/${workspaceKey}/project/${project.slugId}/overview`)
  await page.getByRole('button', { name: 'Setup project notifications' }).click()
  const panel = page.locator('.project-notifications')
  await expect(panel).toBeVisible()
  await page.waitForTimeout(300)
  const measured = await panel.evaluate(root => {
    const css = (el: Element, ...props: string[]) => Object.fromEntries(props.map(prop => [prop, getComputedStyle(el).getPropertyValue(prop)]))
    const height = (el: Element) => Math.round(el.getBoundingClientRect().height * 100) / 100
    const schedule = root.querySelector('.project-notifications__schedule')!
    const slack = root.querySelector('.project-notifications__slack')!
    const icon = slack.querySelector('strong > img, strong > svg')!.getBoundingClientRect()
    return {
      width: root.getBoundingClientRect().width,
      sections: [...root.querySelectorAll('.project-notifications__section, .project-notifications__schedule, .project-notifications__slack')].map(section => css(section, 'padding-top', 'padding-left')),
      schedule: { gap: getComputedStyle(schedule.firstElementChild!).rowGap, title: css(schedule.querySelector('strong')!, 'font-size', 'font-weight', 'line-height'), button: { height: height(schedule.querySelector('button')!), ...css(schedule.querySelector('button')!, 'font-size', 'font-weight', 'border-top-left-radius', 'padding-left') } },
      slack: { title: css(slack.querySelector('strong')!, 'font-size', 'font-weight', 'line-height', 'column-gap'), icon: [icon.width, icon.height], button: { height: height(slack.querySelector('button')!), ...css(slack.querySelector('button')!, 'font-size', 'font-weight', 'border-top-left-radius', 'padding-left') } },
    }
  })
  expect(measured.width).toBeCloseTo(334.21, 1)
  for (const section of measured.sections) expect(section).toEqual({ 'padding-top': '16px', 'padding-left': '16px' })
  expect(measured.schedule.gap).toBe('8px')
  expect(measured.schedule.title).toEqual({ 'font-size': '12px', 'font-weight': '500', 'line-height': '14.5px' })
  expect(measured.schedule.button).toEqual({ height: 24, 'font-size': '12px', 'font-weight': '500', 'border-top-left-radius': '9999px', 'padding-left': '8px' })
  expect(measured.slack.title).toEqual({ 'font-size': '12px', 'font-weight': '500', 'line-height': '14.5px', 'column-gap': '6px' })
  expect(measured.slack.icon).toEqual([16, 16])
  expect(measured.slack.button).toEqual({ height: 24, 'font-size': '12px', 'font-weight': '500', 'border-top-left-radius': '9999px', 'padding-left': '8px' })
})
