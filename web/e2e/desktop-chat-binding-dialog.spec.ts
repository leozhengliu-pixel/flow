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

      // Selects are the shared settings select; the notification options are the panel's checkboxes and the Flow-only options the shared switch.
      const selects = form.locator('button.settings-select')
      expect(await selects.count()).toBeGreaterThanOrEqual(3)
      const toggles = form.locator('input[data-control="toggle"]')
      expect(await toggles.count()).toBeGreaterThanOrEqual(5)
      expect(await form.locator('.chat-group-dialog__options button[role="checkbox"]').count()).toBe(6)

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
        const stacked = root.querySelector('.feature-row:not(.chat-inline)')!
        const stackedCopy = stacked.querySelector('div')!.getBoundingClientRect()
        const stackedControl = stacked.querySelector('aside')!.getBoundingClientRect()
        const inline = root.querySelector('.feature-row.chat-inline')!
        const inlineAside = inline.querySelector('aside')!.getBoundingClientRect()
        const inlineCopy = inline.querySelector('div')!.getBoundingClientRect()
        return { outside, bigIcons: chevrons.length, selectHeight: select.height, selectWidth: select.width, toggleWidth: toggle.width, toggleHeight: toggle.height, stackedGap: stackedControl.top - stackedCopy.bottom, stackedLeft: [stackedCopy.left, stackedControl.left], inlineGap: inlineAside.left - inlineCopy.right, inlineSameRow: Math.abs(inlineAside.top + inlineAside.height / 2 - (inlineCopy.top + inlineCopy.height / 2)) < 1.5, scrolls: root.scrollWidth > root.clientWidth + 1 }
      })
      expect(geometry.outside).toEqual([])
      expect(geometry.bigIcons).toBe(0)
      expect(geometry.scrolls).toBe(false)
      expect(geometry.selectHeight).toBeLessThanOrEqual(36)
      expect(geometry.selectWidth).toBeGreaterThan(80)
      expect([geometry.toggleWidth, geometry.toggleHeight]).toEqual([30, 20])
      // A stacked row (a select) puts the control 8px under its label; an inline row (a switch) keeps the label left of the control.
      expect(geometry.stackedGap).toBe(8)
      expect(geometry.stackedLeft[0]).toBe(geometry.stackedLeft[1])
      expect(geometry.inlineSameRow).toBe(true)
      expect(geometry.inlineGap).toBeGreaterThanOrEqual(24)

      // The reference app's project notification panel: 334.21px wide, 8px radius, 16px sections, 12px titles at 500 and options at 450
      // on 14.5px lines, 14px checkboxes 22.5px apart, 24px pill buttons.
      const panel = await dialog.evaluate(root => {
        const css = (el: Element, ...props: string[]) => Object.fromEntries(props.map(prop => [prop, getComputedStyle(el).getPropertyValue(prop)]))
        const round = (value: number) => Math.round(value * 100) / 100
        const rect = (el: Element) => { const r = el.getBoundingClientRect(); return { width: round(r.width), height: round(r.height) } }
        const options = root.querySelector('.chat-group-dialog__options')!
        const labels = [...options.querySelectorAll('label')]
        const checkboxes = [...options.querySelectorAll('[role="checkbox"]')]
        const buttons = [...root.querySelectorAll('.chat-actions button')]
        return {
          root: { width: root.getBoundingClientRect().width, ...css(root, 'border-top-left-radius', 'padding-top') },
          header: css(root.querySelector('.chat-group-dialog__header')!, 'padding-top', 'padding-right', 'padding-bottom', 'padding-left', 'row-gap'),
          title: css(root.querySelector('.chat-group-dialog__header h2')!, 'font-size', 'font-weight', 'line-height'),
          description: css(root.querySelector('.chat-group-dialog__header p')!, 'font-size', 'font-weight', 'line-height'),
          options: css(options, 'padding-top', 'padding-right', 'padding-bottom', 'padding-left', 'border-bottom-style'),
          optionsTitle: css(options.querySelector('h2')!, 'font-size', 'font-weight', 'line-height', 'margin-bottom'),
          label: css(labels[0], 'font-size', 'font-weight', 'line-height', 'column-gap'),
          pitch: labels.slice(1).map((label, index) => round(label.getBoundingClientRect().top - labels[index].getBoundingClientRect().top)),
          checkbox: { ...rect(checkboxes[0]), ...css(checkboxes[0], 'border-top-left-radius') },
          checkboxRight: round(options.getBoundingClientRect().right - checkboxes[0].getBoundingClientRect().right),
          buttons: buttons.map(button => ({ height: round(button.getBoundingClientRect().height), ...css(button, 'font-size', 'font-weight', 'border-top-left-radius', 'padding-left', 'padding-right') })),
        }
      })
      expect(panel.root.width).toBeCloseTo(334.21, 1)
      expect(panel.root).toMatchObject({ 'border-top-left-radius': '8px', 'padding-top': '0px' })
      expect(panel.header).toEqual({ 'padding-top': '16px', 'padding-right': '48px', 'padding-bottom': '16px', 'padding-left': '16px', 'row-gap': '8px' })
      expect(panel.title).toEqual({ 'font-size': '12px', 'font-weight': '500', 'line-height': '14.5px' })
      expect(panel.description).toEqual({ 'font-size': '12px', 'font-weight': '450', 'line-height': '14.5px' })
      expect(panel.options).toEqual({ 'padding-top': '16px', 'padding-right': '16px', 'padding-bottom': '16px', 'padding-left': '16px', 'border-bottom-style': 'solid' })
      expect(panel.optionsTitle).toEqual({ 'font-size': '12px', 'font-weight': '500', 'line-height': '14.5px', 'margin-bottom': '8px' })
      expect(panel.label).toEqual({ 'font-size': '12px', 'font-weight': '450', 'line-height': '14.5px', 'column-gap': '24px' })
      expect(panel.pitch).toEqual([22.5, 22.5, 22.5, 22.5, 22.5])
      expect(panel.checkbox).toMatchObject({ width: 14, height: 14, 'border-top-left-radius': '3px' })
      expect(panel.checkboxRight).toBe(16)
      expect(panel.buttons.length).toBe(2)
      for (const button of panel.buttons) expect(button).toMatchObject({ height: 24, 'font-size': '12px', 'font-weight': '500', 'border-top-left-radius': '9999px', 'padding-left': '8px', 'padding-right': '8px' })

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
