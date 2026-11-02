import { expect } from '@playwright/test'
import { test } from './workspace-fixture'
import { CHAT_ERROR_MESSAGES } from '../src/lib/chat-integrations-api'
import { chatIntegrationsZhCN } from '../src/i18n/translations-chat-integrations'

const codes = ['chat_code_exchange_failed', 'chat_identity_read_failed', 'chat_oauth_permission_required'] as const

test.use({ timezoneId: 'UTC' })

for (const width of [1440, 375, 390]) {
  for (const theme of ['light', 'dark']) {
    for (const locale of ['en-US', 'zh-CN']) {
      test(`account link errors ${width} ${theme} ${locale}`, async ({ page, workspaceKey }, testInfo) => {
        await page.setViewportSize({ width, height: 900 })
        await page.addInitScript(({ theme, locale }) => {
          localStorage.setItem('flow.theme', JSON.stringify({ interfaceTheme: theme }))
          localStorage.setItem('flow:locale', locale)
        }, { theme, locale })
        await page.route('**/api/bootstrap', async route => {
          const response = await route.fetch()
          const data = await response.json()
          data.viewerRole = 'member'
          data.viewer.name = '张三'
          data.viewer.displayName = '张三'
          data.userSettings[data.viewer.id] = { ...data.userSettings[data.viewer.id], language: locale, interfaceTheme: theme, timezone: 'UTC' }
          data.integrationConnections = [{ id: 'chat-review', provider: 'feishu', name: 'Feishu', status: 'connected' }]
          await route.fulfill({ response, json: data })
        })
        await page.route('**/api/integrations/feishu/chat', route => route.fulfill({ json: {
          connection: { id: 'chat-review', provider: 'feishu', status: 'connected', config: {} },
          identity: null, bindings: [], credentialsConfigured: true, canManage: false,
          permissions: { workspace: { canManage: false }, tenant: { status: 'authorized' }, teams: { manageableTeamIds: [] } },
        } }))
        const errors: string[] = []
        page.on('pageerror', error => errors.push(error.message))
        for (const code of codes) {
          await page.goto(`/${workspaceKey}/settings/account/connections?chat_provider=feishu&chat_auth=error&chat_auth_error=${code}`)
          const row = page.getByTestId('feishu-account-row')
          const alert = row.getByRole('alert')
          const source = CHAT_ERROR_MESSAGES[code]
          await expect(page.locator('html')).toHaveAttribute('data-locale', locale)
          await expect(alert).toContainText(locale === 'zh-CN' ? chatIntegrationsZhCN[source] : source)
          await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
          await expect(page).not.toHaveURL(/chat_auth/)
          const retry = alert.getByRole('button')
          await expect(retry).toBeEnabled()
          await retry.hover()
          await page.keyboard.press('Tab')
          await retry.focus()
          await expect(retry).toBeFocused()
          expect(await retry.evaluate(element => element.matches(':focus-visible'))).toBe(true)
          const geometry = await alert.evaluate(element => {
            const text = element.querySelector('span')!.getBoundingClientRect()
            const button = element.querySelector('button')!.getBoundingClientRect()
            return { textRight: text.right, buttonLeft: button.left, right: element.getBoundingClientRect().right, overflow: document.documentElement.scrollWidth > innerWidth }
          })
          expect(geometry.textRight).toBeLessThanOrEqual(geometry.buttonLeft)
          expect(geometry.right).toBeLessThanOrEqual(width)
          expect(geometry.overflow).toBe(false)
          if (process.env.FLOW_CHAT_REVIEW_SHOTS) {
            await row.screenshot({ path: `${process.env.FLOW_CHAT_REVIEW_SHOTS}/${width}-${theme}-${locale}-${code}.png` })
          }
          await retry.hover()
          await page.mouse.down()
          expect(await retry.evaluate(element => element.matches(':active'))).toBe(true)
          await page.mouse.up()
          await expect(alert).toHaveCount(0)
          await expect(row.getByRole('button', { name: locale === 'zh-CN' ? '连接' : 'Connect', exact: true })).toBeEnabled()
        }
        // Hold the start response so the real control's pending state is visible.
        let releaseStart!: () => void
        const start = new Promise<void>(resolve => { releaseStart = resolve })
        await page.route('**/api/integrations/feishu/chat/identity/start', async route => {
          await start
          await route.fulfill({ status: 502, json: { code: 'chat_code_exchange_failed', error: 'Account authorization failed' } })
        })
        const row = page.getByTestId('feishu-account-row')
        const connect = row.getByRole('button', { name: locale === 'zh-CN' ? '连接' : 'Connect', exact: true })
        try {
          await connect.click()
          await expect(connect).toBeDisabled()
        } finally {
          releaseStart()
        }
        await expect(connect).toBeEnabled()
        const source = CHAT_ERROR_MESSAGES.chat_code_exchange_failed
        await expect(row.getByRole('alert')).toContainText(locale === 'zh-CN' ? chatIntegrationsZhCN[source] : source)
        expect(errors, testInfo.title).toEqual([])
      })
    }
  }
}
