import { expect, test } from '@playwright/test'
import { openNewDocument, pasteText, signUp, startApi, watchViolations, type RunningApi } from './harness'

let api: RunningApi
test.beforeAll(async () => { api = await startApi({ csp: 'enforce' }) })
test.afterAll(async () => { await api?.stop() })

// The players the editor embeds (embed-providers.ts) and the player origin each one's iframe loads.
const players = [
  { name: 'YouTube', link: 'https://www.youtube.com/watch?v=dQw4w9WgXcQ', frame: 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ' },
  { name: 'Loom', link: 'https://www.loom.com/share/0123456789abcdef', frame: 'https://www.loom.com/embed/0123456789abcdef' },
  { name: 'Descript', link: 'https://share.descript.com/view/AbCdEf123456', frame: 'https://share.descript.com/embed/AbCdEf123456' },
  { name: 'Tella', link: 'https://www.tella.tv/video/my-demo-clx8k2m4n0000abc', frame: 'https://www.tella.tv/video/clx8k2m4n0000abc/embed' },
  { name: 'Miro', link: 'https://miro.com/app/board/uXjVKabc123=/', frame: 'https://miro.com/app/embed/uXjVKabc123=/' },
]

test('every supported video and board embed loads under the enforced frame-src', async ({ page, context }) => {
  const account = await signUp(context, api.origin, 'embeds')
  const violations = await watchViolations(page)
  // The provider hosts are not reachable from a test run: answer their requests locally. A frame the policy blocks never
  // reaches this handler, so the handler also proves which origins the browser was willing to contact.
  const requested: string[] = []
  await page.route(/^https:\/\/(www\.youtube-nocookie\.com|www\.loom\.com|share\.descript\.com|www\.tella\.tv|miro\.com)\//, route => {
    requested.push(route.request().url())
    return route.fulfill({ contentType: 'text/html', body: '<!doctype html><title>stub player</title><body><p>stub player</p></body>' })
  })
  const { editor } = await openNewDocument(page, api.origin, account)
  await editor.click()

  for (const player of players) {
    await pasteText(page, player.link)
    // The previous popover fades out for a moment: only the live one carries the action attribute.
    await page.locator('[data-embed-hint-action="embed"]').click()
    const frame = page.locator(`iframe[src="${player.frame}"]`)
    await expect(frame, `${player.name} iframe`).toBeVisible()
    await expect.poll(() => page.frames().some(item => item.url() === player.frame), { message: `${player.name} frame navigated` }).toBe(true)
    await expect(page.frames().find(item => item.url() === player.frame)!.getByText('stub player')).toBeVisible()
    // Fullscreen and picture-in-picture are delegated to the player origin by the Permissions-Policy header.
    expect(await frame.evaluate(element => (element.ownerDocument as unknown as { featurePolicy: { allowsFeature(name: string, origin: string): boolean } }).featurePolicy.allowsFeature('fullscreen', new URL((element as HTMLIFrameElement).src).origin))).toBe(true)
  }
  expect(requested.map(url => new URL(url).origin).sort()).toEqual([...new Set(players.map(player => new URL(player.frame).origin))].sort())
  expect(violations.all()).toEqual([])
})

test('a frame from an origin outside the allow list is blocked, youtube.com itself included', async ({ page, context }) => {
  const account = await signUp(context, api.origin, 'embeds-blocked')
  const violations = await watchViolations(page)
  const reached: string[] = []
  await page.route(/^https:\/\/(evil\.example|www\.youtube\.com)\//, route => {
    reached.push(route.request().url())
    return route.fulfill({ contentType: 'text/html', body: '<!doctype html><p>should not load</p>' })
  })
  await openNewDocument(page, api.origin, account)
  const blocked = await page.evaluate(async () => {
    const results: Array<{ directive: string; blocked: string }> = []
    document.addEventListener('securitypolicyviolation', event => results.push({ directive: event.violatedDirective, blocked: event.blockedURI }))
    // An embed saved with another origin (an edited document, an import) must not turn into a live frame.
    for (const src of ['https://evil.example/player', 'https://www.youtube.com/embed/dQw4w9WgXcQ']) {
      const frame = document.createElement('iframe')
      frame.src = src
      document.body.append(frame)
    }
    await new Promise(resolve => setTimeout(resolve, 500))
    return results
  })
  expect(blocked.map(item => item.directive)).toEqual(['frame-src', 'frame-src'])
  expect(blocked.map(item => item.blocked)).toEqual(['https://evil.example', 'https://www.youtube.com'])
  expect(reached).toEqual([])
  expect(violations.all().filter(text => /frame-src/.test(text))).toHaveLength(2)
})
