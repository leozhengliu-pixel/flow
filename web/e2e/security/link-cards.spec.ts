import { expect, test } from '@playwright/test'
import { apiFetch, openNewDocument, pasteText, signUp, startApi, startStubServer, watchViolations } from './harness'

test('X, Figma, GitHub and GitLab cards fetch through the server under the enforced policy', async ({ page, context }) => {
  const requests: string[] = []
  const fixture = await startStubServer((request, response) => {
    const target = `${request.headers.host}${request.url}`
    requests.push(target)
    if (target === 'publish.x.com/oembed?omit_script=1&dnt=true&url=https%3A%2F%2Fx.com%2Fflow%2Fstatus%2F20') {
      response.setHeader('content-type', 'application/json')
      response.end(JSON.stringify({ author_name: 'Fixture author', html: '<blockquote>Fixture post body</blockquote><script>window.providerScriptRan = true</script>' }))
    } else if (target === 'www.figma.com/design/security/Fixture') {
      response.setHeader('content-type', 'text/html')
      response.end('<head><meta property="og:title" content="Fixture design"><meta property="og:description" content="Design from the server fixture"><meta property="og:image" content="https://static.figma.com/security-cover.png"></head>')
    } else if (target === 'raw.githubusercontent.com/flow/fixture/main/example.ts' || target === 'gitlab.com/api/v4/projects/flow%2Ffixture/repository/files/example.ts/raw?ref=main') {
      response.setHeader('content-type', 'text/plain')
      response.end('hidden first line\nexport const fixture = "server fetched"\nexport const last = true\nhidden last line\n')
    } else if (request.url === '/security-cover.png') {
      response.setHeader('content-type', 'image/png')
      response.end(Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=', 'base64'))
    } else if (target === 'www.figma.com/design/redirect/Fixture') {
      response.writeHead(302, { location: `${fixture.origin}/should-not-fetch` })
      response.end()
    } else {
      response.writeHead(404)
      response.end()
    }
  })
  const api = await startApi({ fixtureOrigin: fixture.origin })
  try {
    const account = await signUp(context, api.origin, 'link-cards')
    const violations = await watchViolations(page)
    const browserProviders: string[] = []
    page.on('request', request => { if (new URL(request.url()).origin !== api.origin) browserProviders.push(request.url()) })
    await page.route('https://static.figma.com/security-cover.png', async route => {
      const image = await context.request.get(`${fixture.origin}/security-cover.png`)
      await route.fulfill({ response: image })
    })
    const { editor } = await openNewDocument(page, api.origin, account)
    await editor.click()
    for (const link of [
      'https://x.com/flow/status/20',
      'https://www.figma.com/design/security/Fixture',
      'https://github.com/flow/fixture/blob/main/example.ts#L2-L3',
      'https://gitlab.com/flow/fixture/-/blob/main/example.ts#L2-3',
    ]) {
      await pasteText(page, link)
      await page.locator('[data-embed-hint-action="embed"]').click()
    }
    await expect(page.locator('[data-link-preview="ready"]')).toHaveCount(2)
    await expect(page.getByText('Fixture author on Twitter / X', { exact: true })).toBeVisible()
    await expect(page.getByText('Fixture post body', { exact: false })).toBeVisible()
    const design = page.locator('[data-link-preview="ready"]').filter({ hasText: 'Fixture design' })
    await expect(design).toHaveAttribute('target', '_blank')
    await expect(design).toHaveAttribute('rel', 'noopener noreferrer')
    await expect(design.locator('img')).toHaveAttribute('referrerpolicy', 'no-referrer')
    await expect.poll(() => design.locator('img').evaluate(image => (image as HTMLImageElement).naturalWidth)).toBe(1)
    const files = page.locator('[data-file-preview="ready"]')
    await expect(files).toHaveCount(2)
    for (const file of await files.all()) {
      await expect(file).toContainText('export const fixture = "server fetched"')
      await expect(file).toContainText('export const last = true')
      await expect(file).not.toContainText('hidden')
      await expect(file).toContainText('L2-L3')
    }
    await expect(page.locator('iframe')).toHaveCount(0)
    expect(await page.evaluate(() => 'providerScriptRan' in window)).toBe(false)
    expect(browserProviders).toEqual(['https://static.figma.com/security-cover.png'])
    expect(requests).toEqual(expect.arrayContaining([
      expect.stringContaining('publish.x.com/oembed?'), 'www.figma.com/design/security/Fixture',
      'raw.githubusercontent.com/flow/fixture/main/example.ts',
      'gitlab.com/api/v4/projects/flow%2Ffixture/repository/files/example.ts/raw?ref=main',
    ]))

    const before = requests.length
    const rejected = await apiFetch(page, 'GET', `/api/integrations/link-preview?url=${encodeURIComponent(`${fixture.origin}/design/security/Fixture`)}`, undefined, account.workspaceKey)
    expect(rejected.status).toBe(400)
    expect(rejected.json?.code).toBe('invalid_url')
    const unconnected = await apiFetch(page, 'GET', `/api/integrations/file-preview?url=${encodeURIComponent(`${fixture.origin}/flow/fixture/-/blob/main/example.ts`)}`, undefined, account.workspaceKey)
    expect(unconnected.status).toBe(404)
    expect(unconnected.json?.code).toBe('not_connected')
    expect(requests).toHaveLength(before)
    const redirect = await apiFetch(page, 'GET', `/api/integrations/link-preview?url=${encodeURIComponent('https://www.figma.com/design/redirect/Fixture')}`, undefined, account.workspaceKey)
    expect(redirect.status).toBe(502)
    expect(redirect.json?.code).toBe('upstream')
    expect(requests.slice(before)).toEqual(['www.figma.com/design/redirect/Fixture'])
    expect(violations.all()).toEqual([])
  } finally {
    await api.stop()
    await fixture.stop()
  }
})
