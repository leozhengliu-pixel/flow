import { expect, test } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'
import { signUp, startApi, startProxy, watchViolations, type RunningApi } from './harness'

let api: RunningApi
test.beforeAll(async () => { api = await startApi({ csp: 'enforce' }) })
test.afterAll(async () => { await api?.stop() })

// public/sw.js is registered by the production build only (src/lib/service-worker.ts), so this needs the built app the Go
// server serves, not the dev server. It caches content-hashed /assets/ files cache-first.
test('the asset service worker registers, activates and serves from its cache under the enforced policy', async ({ page, context }) => {
  const account = await signUp(context, api.origin, 'service-worker')
  const violations = await watchViolations(page)
  const workerMessages: string[] = []
  context.on('console', message => { if (message.type() === 'error' || message.type() === 'warning') workerMessages.push(message.text()) })

  const script = await page.goto(`${api.origin}/sw.js`)
  expect(script!.status()).toBe(200)
  expect(script!.headers()['content-type']).toMatch(/javascript/)
  expect(script!.headers()['content-security-policy']).toContain("worker-src 'self'")

  await page.goto(`${api.origin}/${account.workspaceKey}/issues/all`)
  await expect(page.locator('main').first()).toBeVisible()
  // Registration waits for the load event; the worker claims the page on activation.
  const registration = await page.evaluate(async () => {
    const ready = await navigator.serviceWorker.ready
    return { scope: ready.scope, script: ready.active?.scriptURL, state: ready.active?.state }
  })
  expect(registration.scope).toBe(`${api.origin}/`)
  expect(registration.script).toBe(`${api.origin}/sw.js`)
  expect(registration.state).toBe('activated')

  // The worker controls the page after a reload, and the hashed build files that load are put in its cache.
  await page.reload()
  await expect(page.locator('main').first()).toBeVisible()
  await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller?.scriptURL)).toBe(`${api.origin}/sw.js`)
  await expect.poll(async () => page.evaluate(async () => (await (await caches.open('flow-assets-v1')).keys()).length), { timeout: 15_000 }).toBeGreaterThan(0)
  const cached = await page.evaluate(async () => (await (await caches.open('flow-assets-v1')).keys()).map(request => new URL(request.url).pathname))
  expect(cached.every(path => path.startsWith('/assets/'))).toBe(true)

  // Offline, a cached asset still answers (the worker's fetch handler ran), and a file it never cached fails.
  await context.setOffline(true)
  try {
    const offline = await page.evaluate(async path => {
      const hit = await fetch(path)
      let miss = 'resolved'
      try { await fetch('/assets/never-cached-AbCdEf12.js') } catch { miss = 'failed' }
      return { status: hit.status, miss }
    }, cached[0])
    expect(offline).toEqual({ status: 200, miss: 'failed' })
  } finally {
    await context.setOffline(false)
  }

  expect(violations.all()).toEqual([])
  expect(workerMessages.filter(text => /content security policy|service worker/i.test(text))).toEqual([])
})

test('worker-src still refuses a worker that is not a file of the app', async ({ page, context }) => {
  const account = await signUp(context, api.origin, 'worker-src')
  const violations = await watchViolations(page)
  await page.goto(`${api.origin}/${account.workspaceKey}/issues/all`)
  await expect(page.locator('main').first()).toBeVisible()
  const refused = await page.evaluate(() => new Promise<string>(resolve => {
    document.addEventListener('securitypolicyviolation', event => resolve(`${event.violatedDirective} ${event.blockedURI}`))
    try { new Worker(URL.createObjectURL(new Blob(['self.close()']))) } catch (error) { resolve(`threw ${(error as Error).name}`) }
    setTimeout(() => resolve('allowed'), 3000)
  }))
  expect(refused).toBe('worker-src blob')
  expect(violations.all().some(text => /worker-src/.test(text))).toBe(true)
})

test('a new worker version activates without closing the page and retires its old cache', async ({ page, context }) => {
  let script = fs.readFileSync(path.join(process.env.FLOW_SECURITY_WEB_DIST!, 'sw.js'), 'utf8')
  const policy = (await context.request.get(`${api.origin}/sw.js`)).headers()['content-security-policy']
  const gateway = await startProxy(api.port, { intercept: { '/sw.js': (_request, response) => {
    response.setHeader('content-type', 'application/javascript')
    response.setHeader('content-security-policy', policy)
    response.setHeader('cache-control', 'no-store')
    response.end(script)
  } } })
  const origin = `http://localhost:${gateway.port}`
  try {
    const account = await signUp(context, origin, 'worker-update')
    const violations = await watchViolations(page)
    await page.goto(`${origin}/${account.workspaceKey}/issues/all`)
    await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller?.state)).toBe('activated')
    await page.evaluate(async () => { await (await caches.open('flow-assets-v1')).put('/assets/old-AbCdEf12.js', new Response('old')) })
    script = script.replace("'flow-assets-v1'", "'flow-assets-security-v2'")
    const updated = await page.evaluate(async () => {
      const registration = (await navigator.serviceWorker.getRegistration())!
      const previous = registration.active!
      const changed = new Promise<void>(resolve => {
        navigator.serviceWorker.addEventListener('controllerchange', () => resolve(), { once: true })
      })
      await registration.update()
      await changed
      return { previous: previous.state, waiting: registration.waiting?.state }
    })
    expect(updated).toEqual({ previous: 'redundant', waiting: undefined })
    // controllerchange can precede completion of the new worker's activate event.
    await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller?.state)).toBe('activated')
    await expect.poll(() => page.evaluate(() => caches.has('flow-assets-v1'))).toBe(false)
    await page.reload()
    await expect(page.locator('main').first()).toBeVisible()
    await expect.poll(() => page.evaluate(async () => (await (await caches.open('flow-assets-security-v2')).keys()).length)).toBeGreaterThan(0)
    expect(violations.all()).toEqual([])
  } finally {
    await gateway.stop()
  }
})

test('the browser kill switch unregisters the worker and clears only Flow asset caches', async ({ page, context }) => {
  const account = await signUp(context, api.origin, 'worker-disable')
  const violations = await watchViolations(page)
  await page.goto(`${api.origin}/${account.workspaceKey}/issues/all`)
  await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller?.state)).toBe('activated')
  await page.evaluate(async () => {
    await (await caches.open('flow-assets-v1')).put('/assets/old-AbCdEf12.js', new Response('old'))
    await caches.open('unrelated-cache')
    localStorage.setItem('flow:disable-sw', '1')
  })
  await page.reload()
  await expect(page.locator('main').first()).toBeVisible()
  await expect.poll(() => page.evaluate(async () => (await navigator.serviceWorker.getRegistrations()).length)).toBe(0)
  // An unregistered worker still controls this client and can repopulate caches
  // for late chunks. The next navigation drops it and completes cleanup.
  await page.reload()
  await expect(page.locator('main').first()).toBeVisible()
  expect(await page.evaluate(() => navigator.serviceWorker.controller)).toBeNull()
  await expect.poll(() => page.evaluate(async () => (await caches.keys()).filter(key => key.startsWith('flow-assets-')))).toEqual([])
  expect(await page.evaluate(() => caches.has('unrelated-cache'))).toBe(true)
  expect(violations.all()).toEqual([])
})

test('a deployed kill-switch script unregisters on the next update check', async ({ page, context }) => {
  let script = fs.readFileSync(path.join(process.env.FLOW_SECURITY_WEB_DIST!, 'sw.js'), 'utf8')
  const policy = (await context.request.get(`${api.origin}/sw.js`)).headers()['content-security-policy']
  const gateway = await startProxy(api.port, { intercept: { '/sw.js': (_request, response) => {
    response.setHeader('content-type', 'application/javascript')
    response.setHeader('content-security-policy', policy)
    response.setHeader('cache-control', 'no-store')
    response.end(script)
  } } })
  const origin = `http://localhost:${gateway.port}`
  try {
    const violations = await watchViolations(page)
    // Keep app bootstrap registration out of the update/unregister lifecycle being tested.
    await page.goto(`${origin}/sw.js`)
    await page.evaluate(async () => {
      await navigator.serviceWorker.register('/sw.js')
      await navigator.serviceWorker.ready
    })
    await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller?.state)).toBe('activated')
    script = "self.addEventListener('install', () => self.skipWaiting()); self.addEventListener('activate', event => event.waitUntil(self.registration.unregister()));"
    await page.evaluate(async () => { await (await navigator.serviceWorker.getRegistration())!.update() })
    await expect.poll(() => page.evaluate(async () => (await navigator.serviceWorker.getRegistrations()).length)).toBe(0)
    // Inspect a non-app navigation so its bootstrap cannot register the retired script again.
    await page.goto(`${origin}/sw.js`)
    expect(await page.evaluate(() => navigator.serviceWorker.controller)).toBeNull()
    expect(violations.all()).toEqual([])
  } finally {
    await gateway.stop()
  }
})
