import { expect, test, type CDPSession } from '@playwright/test'
import { apiFetch, signUp, startApi, watchViolations, type RunningApi } from './harness'

// A passkey is Flow's second factor: Settings registers one (navigator.credentials.create), and a workspace that requires
// two-factor authentication asks for one on sign-in (navigator.credentials.get). Chromium's virtual authenticator stands in
// for the hardware. The WebAuthn relying party is the page's host name, so these run on localhost, not an IP address.

let api: RunningApi
test.beforeAll(async () => { api = await startApi({ csp: 'enforce' }) })
test.afterAll(async () => { await api?.stop() })

async function virtualAuthenticator(cdp: CDPSession) {
  await cdp.send('WebAuthn.enable')
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true },
  })
  return authenticatorId
}

test('registers a passkey and verifies the second factor with it under the enforced policy', async ({ page, context }) => {
  const account = await signUp(context, api.origin, 'passkey')
  const violations = await watchViolations(page)
  const cdp = await context.newCDPSession(page)
  const authenticatorId = await virtualAuthenticator(cdp)
  const credentials = async () => (await cdp.send('WebAuthn.getCredentials', { authenticatorId })).credentials

  // navigator.credentials.create from Settings -> Security & access. The headers that gate the ceremony: the enforced
  // policy, and both passkey features delegated to this origin by the Permissions-Policy.
  const response = await page.goto(`${api.origin}/${account.workspaceKey}/settings/account/security`)
  expect(response!.headers()['content-security-policy']).toContain("script-src 'self'")
  expect(response!.headers()['permissions-policy']).toContain('publickey-credentials-create=(self)')
  expect(response!.headers()['permissions-policy']).toContain('publickey-credentials-get=(self)')
  await expect(page.getByText('Passkeys', { exact: true }).first()).toBeVisible()
  expect(await page.evaluate(() => ((document as unknown as { featurePolicy: { allowsFeature(name: string): boolean } }).featurePolicy).allowsFeature('publickey-credentials-create'))).toBe(true)
  await page.getByRole('button', { name: 'New passkey' }).first().click()
  await expect(page.getByText('Passkey added')).toBeVisible()
  await expect.poll(async () => (await credentials()).length).toBe(1)
  const registered = (await credentials())[0]
  expect(registered.rpId).toBe('localhost')

  // The workspace now requires a second factor; the session has none, so the app shows the verification page and
  // navigator.credentials.get signs the server's challenge with the registered passkey.
  expect((await apiFetch(page, 'PATCH', '/api/workspace/preferences', { requireTwoFactor: true }, account.workspaceKey)).status).toBe(200)
  expect((await apiFetch(page, 'GET', '/api/bootstrap', undefined, account.workspaceKey)).status).toBe(403)
  await page.goto(`${api.origin}/${account.workspaceKey}/issues/all`)
  await expect(page.getByRole('heading', { name: 'Verify your identity' })).toBeVisible()
  await page.getByRole('button', { name: 'Verify with passkey' }).click()
  await expect(page.getByRole('heading', { name: 'Verify your identity' })).toBeHidden()
  await expect(page.locator('main').first()).toBeVisible()
  await expect.poll(async () => (await credentials())[0].signCount).toBeGreaterThan(registered.signCount)
  expect((await apiFetch(page, 'GET', '/api/bootstrap', undefined, account.workspaceKey)).status).toBe(200)

  expect(violations.all()).toEqual([])
})

test('enrols and verifies a first passkey from the verification page', async ({ page, context }) => {
  const account = await signUp(context, api.origin, 'passkey-enrol')
  const violations = await watchViolations(page)
  const cdp = await context.newCDPSession(page)
  const authenticatorId = await virtualAuthenticator(cdp)
  await page.goto(`${api.origin}/sw.js`)
  expect((await apiFetch(page, 'PATCH', '/api/workspace/preferences', { requireTwoFactor: true }, account.workspaceKey)).status).toBe(200)
  await page.goto(`${api.origin}/${account.workspaceKey}/issues/all`)
  await page.getByRole('button', { name: 'Verify with passkey' }).click()
  // No passkey yet: the page asks to register one, then verifies with it.
  await page.getByRole('button', { name: 'Register passkey' }).click()
  await expect(page.getByRole('heading', { name: 'Verify your identity' })).toBeHidden()
  await expect(page.locator('main').first()).toBeVisible()
  expect((await cdp.send('WebAuthn.getCredentials', { authenticatorId })).credentials).toHaveLength(1)
  expect(violations.all()).toEqual([])
})
