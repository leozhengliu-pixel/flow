import { expect, test } from '@playwright/test'
import { openNewDocument, signUp, startApi, watchViolations, type RunningApi } from './harness'

let api: RunningApi
test.beforeAll(async () => { api = await startApi({ csp: 'enforce' }) })
test.afterAll(async () => { await api?.stop() })

// Flow has no math syntax of its own (no $...$ in prose): KaTeX is what Mermaid uses to typeset a `$$...$$` label inside a
// ```mermaid block, so that is where math reaches the page. The block renders as an SVG whose labels are KaTeX output.
test('Mermaid math (KaTeX) renders in a document without a policy violation', async ({ page, context }) => {
  const account = await signUp(context, api.origin, 'math')
  const violations = await watchViolations(page)
  const failedRequests: string[] = []
  page.on('requestfailed', request => { if (!/ERR_ABORTED/.test(request.failure()?.errorText ?? '')) failedRequests.push(`${request.failure()?.errorText} ${request.url()}`) })

  const { editor } = await openNewDocument(page, api.origin, account)
  await editor.click()
  await page.keyboard.type('```mermaid ')
  // Inline math in a node label, and a fraction (a block-style formula) in the second node and on the edge.
  await page.keyboard.type('flowchart LR\n  A["$$x^2 + y^2 = z^2$$"] -->|"$$\\sqrt{n}$$"| B["$$\\frac{a}{b}$$"]')
  await page.getByRole('button', { name: 'Show diagram' }).click()

  const diagram = page.locator('.flow-diagram__svg svg')
  await expect(diagram).toBeVisible()
  // Mermaid asks KaTeX for MathML only (no KaTeX stylesheet and no web fonts), one <math> per label, laid out by the
  // browser: a fraction and a root among them.
  await expect(diagram.locator('.katex math')).toHaveCount(3)
  await expect(diagram.locator('.katex msup')).toHaveCount(3)
  await expect(diagram.locator('.katex mfrac')).toHaveCount(1)
  await expect(diagram.locator('.katex msqrt')).toHaveCount(1)
  for (const math of await diagram.locator('.katex math').all()) expect((await math.boundingBox())?.width ?? 0).toBeGreaterThan(4)
  // The diagram's own inline styles (its <style> element and style attributes) are what style-src 'unsafe-inline' is for.
  expect(await diagram.locator('style').count()).toBeGreaterThan(0)
  await page.evaluate(() => document.fonts.ready)
  expect(await page.evaluate(() => [...document.fonts].filter(font => font.status === 'error').map(font => font.family))).toEqual([])
  expect(failedRequests).toEqual([])
  expect(violations.all()).toEqual([])
})
