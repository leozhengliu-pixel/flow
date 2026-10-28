import http from 'node:http'
import { expect, test, type Page } from '@playwright/test'
import { freePort, signUp, startApi, startProxy, startStubServer, watchViolations } from './harness'

// Flow has no report collector of its own (docs/configuration.md): FLOW_SECURITY_CSP_REPORT_URI points at one the operator
// runs, an https URL or a path their gateway routes. These tests are that collector.
type Report = { contentType: string; body: unknown }

function collector() {
  const reports: Report[] = []
  const listener: http.RequestListener = (request, response) => {
    const chunks: Buffer[] = []
    request.on('data', chunk => chunks.push(chunk))
    request.on('end', () => {
      const text = Buffer.concat(chunks).toString('utf8')
      let body: unknown = text
      try { body = JSON.parse(text) } catch { /* keep the text */ }
      if (request.method === 'POST') reports.push({ contentType: String(request.headers['content-type'] ?? ''), body })
      response.statusCode = 204
      response.end()
    })
  }
  return { reports, listener }
}

/** The violation entries of every report received, from both wire formats: the legacy report-uri one and the Reporting API one. */
function violationsIn(reports: Report[]) {
  const entries: Array<Record<string, unknown>> = []
  for (const report of reports) {
    const body = report.body as { 'csp-report'?: Record<string, unknown> } | Array<{ type?: string; body?: Record<string, unknown> }>
    if (Array.isArray(body)) for (const item of body) { if (item.type === 'csp-violation' && item.body) entries.push(item.body) }
    else if (body?.['csp-report']) entries.push(body['csp-report'])
  }
  return entries.map(entry => ({
    directive: String(entry.effectiveDirective ?? entry['effective-directive'] ?? ''),
    blocked: String(entry.blockedURL ?? entry['blocked-uri'] ?? ''),
    disposition: String(entry.disposition ?? ''),
    document: String(entry.documentURL ?? entry['document-uri'] ?? ''),
  }))
}

/** An inline script is the textbook violation: the web app's script-src is 'self' plus the hash of its one bootstrap script. */
async function injectInlineScript(page: Page) {
  await page.evaluate(() => {
    const script = document.createElement('script')
    script.textContent = 'window.__injectedScriptRan = true'
    document.head.append(script)
  })
  return await page.evaluate(() => (window as unknown as { __injectedScriptRan?: boolean }).__injectedScriptRan === true)
}

test('report-only mode runs the violating script and reports it to an http loopback collector', async ({ page, context }) => {
  const { reports, listener } = collector()
  const stub = await startStubServer(listener)
  const api = await startApi({ csp: 'report-only', reportUri: `${stub.origin}/csp-reports` })
  try {
    const account = await signUp(context, api.origin, 'report-only')
    const violations = await watchViolations(page)
    const response = await page.goto(`${api.origin}/${account.workspaceKey}/issues/all`)
    const headers = response!.headers()
    // An http endpoint gets report-uri alone: Chromium never delivers report-to reports to http and then ignores report-uri.
    expect(headers['content-security-policy-report-only']).toMatch(new RegExp(`; report-uri ${stub.origin}/csp-reports$`))
    expect(headers['reporting-endpoints']).toBeUndefined()
    // The enforced header carries only framing: report-only must not block anything.
    expect(headers['content-security-policy']).toBe("frame-ancestors 'self'")
    await expect(page.locator('main').first()).toBeVisible()
    expect(violations.all(), 'the app itself raises no report').toEqual([])
    expect(reports).toEqual([])

    expect(await injectInlineScript(page), 'report-only does not block the script').toBe(true)
    await expect.poll(() => violationsIn(reports).length, { timeout: 15_000 }).toBeGreaterThan(0)
    expect(reports[0].contentType).toBe('application/csp-report')
    expect(violationsIn(reports)[0]).toMatchObject({ directive: 'script-src-elem', blocked: 'inline', disposition: 'report' })
    expect(violationsIn(reports)[0].document).toContain(`${api.origin}/${account.workspaceKey}/issues/all`)
    expect(violations.all().some(text => /Executing inline script violates.*script-src.*The policy is report-only/.test(text))).toBe(true)
  } finally {
    await api.stop()
    await stub.stop()
  }
})

test('behind an https gateway the report path on the app origin is delivered through the Reporting API, and enforce mode blocks the script', async ({ page, context }) => {
  test.setTimeout(120_000)
  const { reports, listener } = collector()
  const gatewayPort = await freePort()
  const publicOrigin = `https://flow-public.localhost:${gatewayPort}`
  const api = await startApi({ csp: 'enforce', appUrl: publicOrigin, trustProxy: true, reportUri: '/csp-reports' })
  const gateway = await startProxy(api.port, { port: gatewayPort, tls: true, intercept: { '/csp-reports': listener } })
  try {
    const account = await signUp(context, publicOrigin, 'report-enforce')
    const violations = await watchViolations(page)
    const response = await page.goto(`${publicOrigin}/${account.workspaceKey}/issues/all`)
    const headers = response!.headers()
    expect(headers['content-security-policy']).toContain(`; report-uri ${publicOrigin}/csp-reports; report-to csp`)
    expect(headers['reporting-endpoints']).toBe(`csp="${publicOrigin}/csp-reports"`)
    expect(headers['strict-transport-security']).toBeTruthy()
    await expect(page.locator('main').first()).toBeVisible()
    expect(violations.all()).toEqual([])

    expect(await injectInlineScript(page), 'enforce mode blocks the script').toBe(false)
    // Chrome sends Reporting API reports as one batch a few seconds later.
    await expect.poll(() => violationsIn(reports).length, { timeout: 60_000, intervals: [500, 1000, 2000] }).toBeGreaterThan(0)
    expect(reports[0].contentType).toBe('application/reports+json')
    expect(violationsIn(reports)[0]).toMatchObject({ directive: 'script-src-elem', blocked: 'inline', disposition: 'enforce' })
    expect(violations.all().some(text => /Executing inline script violates.*script-src/.test(text) && !/report-only/.test(text))).toBe(true)
  } finally {
    await gateway.stop()
    await api.stop()
  }
})
