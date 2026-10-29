import { expect, test } from '@playwright/test'
import { embedSourceForUrl } from '../../src/components/editor/embeds/embed-providers'
import { openNewDocument, pasteText, signUp, startApi, type RunningApi } from './harness'

test.skip(process.env.FLOW_SECURITY_LIVE_EMBEDS !== '1', 'Set FLOW_SECURITY_LIVE_EMBEDS=1 for real-provider checks')

let api: RunningApi
test.beforeAll(async () => { api = await startApi() })
test.afterAll(async () => { await api?.stop() })

for (const [provider, link] of [
  ['YouTube', process.env.FLOW_SECURITY_LIVE_YOUTUBE_URL ?? 'https://www.youtube.com/watch?v=dQw4w9WgXcQ'],
  ['Loom', process.env.FLOW_SECURITY_LIVE_LOOM_URL],
  ['Descript', process.env.FLOW_SECURITY_LIVE_DESCRIPT_URL],
  ['Tella', process.env.FLOW_SECURITY_LIVE_TELLA_URL],
  ['Miro', process.env.FLOW_SECURITY_LIVE_MIRO_URL],
] as const) {
  test(`${provider} loads a real provider frame under the enforced policy`, async ({ page, context }) => {
    test.skip(!link, `Set FLOW_SECURITY_LIVE_${provider.toUpperCase()}_URL to a public share link`)
    const source = embedSourceForUrl(link!)
    expect(source?.kind).toBe('iframe')
    const frameUrl = source!.embedUrl
    try {
      await context.request.get(new URL(frameUrl).origin, { timeout: 10_000 })
    } catch {
      test.skip(true, `${provider} is unreachable (offline)`)
    }
    const account = await signUp(context, api.origin, `live-${provider.toLowerCase()}`)
    const { editor } = await openNewDocument(page, api.origin, account)
    await page.evaluate(() => {
      const violations: string[] = []
      Object.assign(window, { appPolicyViolations: violations })
      document.addEventListener('securitypolicyviolation', event => violations.push(`${event.effectiveDirective}: ${event.blockedURI}`))
    })
    await editor.click()
    await pasteText(page, link!)
    const navigation = page.waitForResponse(response => {
      if (!response.request().isNavigationRequest() || response.status() >= 300 && response.status() < 400) return false
      let request = response.request()
      while (request.redirectedFrom()) request = request.redirectedFrom()!
      return request.url() === frameUrl
    })
    await page.locator('[data-embed-hint-action="embed"]').click()
    const response = await navigation
    expect(response.ok(), `${provider} response: ${response.status()}`).toBe(true)
    await expect(page.locator(`iframe[src="${frameUrl}"]`)).toBeVisible()
    await expect.poll(() => page.frames().some(frame => frame.url().startsWith(new URL(frameUrl).origin))).toBe(true)
    expect(await page.evaluate(() => (window as unknown as { appPolicyViolations: string[] }).appPolicyViolations)).toEqual([])
  })
}
