import { expect } from '@playwright/test'
import { randomUUID } from 'node:crypto'
import { test as workspaceTest } from '../workspace-fixture'

const test = workspaceTest.extend({
  request: async ({ context }, provide) => {
    const identity = { email: `${randomUUID()}@example.test`, password: 'fixture-password' }
    const request = context.request
    const registered = await request.post('/api/auth/register', { data: { ...identity, name: 'Telemetry Fixture' } })
    expect(registered.status()).toBe(201)
    const { verificationToken } = await registered.json() as { verificationToken: string }
    expect((await request.post('/api/auth/verify-email', { data: { token: verificationToken } })).status()).toBe(200)
    expect((await request.post('/api/auth/login', { data: identity })).status()).toBe(200)
    await provide(request)
  },
})

test('keeps anonymous pages out of telemetry', async ({ page }) => {
  const config = page.waitForResponse(response => response.url().endsWith('/api/auth/providers'))
  await page.goto('/login')
  expect((await config).headers()['flow-telemetry']).toBeUndefined()
  const result = await page.evaluate(async () => {
    const { activeBrowserTelemetry } = await import('/src/lib/browser-telemetry.ts' as string)
    const response = await fetch('/api/telemetry', {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '[]',
    })
    return { active: Boolean(activeBrowserTelemetry()), status: response.status }
  })
  expect(result).toEqual({ active: false, status: 401 })
})

test('forwards authenticated pagehide telemetry through the same-origin intake', async ({ page, workspaceKey }, testInfo) => {
  const config = page.waitForResponse(response => response.url().endsWith('/api/auth/session'))
  await page.goto(`/${workspaceKey}/my-issues/assigned`)
  await expect(page.getByRole('main')).toBeVisible()
  await expect(page.getByText('No issues assigned to you', { exact: true })).toBeVisible()
  expect((await config).headers()['flow-telemetry']).toBe('/api/telemetry')
  expect((await config).headers()['flow-telemetry-sample-rate']).toBe('1')

  const recordError = () => page.evaluate(async () => {
    const { activeBrowserTelemetry } = await import('/src/lib/browser-telemetry.ts' as string)
    for (let i = 0; i < 100 && !activeBrowserTelemetry(); i++) {
      await new Promise(resolve => setTimeout(resolve, 20))
    }
    const telemetry = activeBrowserTelemetry()
    if (!telemetry) throw new Error('Browser observers did not start')
    await telemetry.error(new Error('telemetry-fixture-email@example.test token=telemetry-fixture-token'))
  })

  await page.screenshot({ path: testInfo.outputPath('my-issues.png') })
  await recordError()
  const batch = page.waitForResponse(response => response.url().endsWith('/api/telemetry') && response.request().postData()?.includes('"js_error"') === true)
  await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent('pagehide')))
  const response = await batch
  expect(response.status()).toBe(204)
  expect((await response.request().headerValue('cookie'))?.includes('flow_clone_session=')).toBe(true)
  expect(await response.request().headerValue('referer')).toBeNull()
  const data = response.request().postData()
  expect(data).not.toContain(workspaceKey)
  expect(data).not.toContain('telemetry-fixture')
  const observed = async () => (await fetch('http://127.0.0.1:4481/observed')).json()
  await expect.poll(observed).toEqual({ browserErrors: 1, leaked: false })

  await recordError()
  await page.goto('about:blank')
  await expect.poll(observed).toEqual({ browserErrors: 2, leaked: false })
})
