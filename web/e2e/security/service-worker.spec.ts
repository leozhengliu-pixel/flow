import { expect, test } from '@playwright/test'
import fs from 'node:fs'
import http from 'node:http'
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
  await expect.poll(async () => page.evaluate(async () => (await (await caches.open('flow-assets-v2')).keys()).length), { timeout: 15_000 }).toBeGreaterThan(0)
  const cached = await page.evaluate(async () => (await (await caches.open('flow-assets-v2')).keys()).map(request => new URL(request.url).pathname))
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
    await page.evaluate(async () => { await (await caches.open('flow-assets-v2')).put('/assets/old-AbCdEf12.js', new Response('old')) })
    script = script.replace("'flow-assets-v2'", "'flow-assets-security-v2'")
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
    await expect.poll(() => page.evaluate(() => caches.has('flow-assets-v2'))).toBe(false)
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
    await (await caches.open('flow-assets-v2')).put('/assets/old-AbCdEf12.js', new Response('old'))
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
  await expect.poll(() => page.evaluate(async () => (await caches.keys()).filter(key => /^flow-(?:assets|shell)-/.test(key)))).toEqual([])
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

test('navigations get the cached app shell at once, a new deploy is used from the next load, server and sign-in routes never', async ({ page, context }) => {
  let marker = ''
  const gateway = await startProxy(api.port, { intercept: { '/': (request, response) => {
    // The root page as the server sends it, plus a marker standing in for a new deploy's HTML.
    http.get({ host: '127.0.0.1', port: api.port, path: '/', headers: { cookie: request.headers.cookie ?? '' } }, upstream => {
      let body = ''
      upstream.setEncoding('utf8')
      upstream.on('data', chunk => { body += chunk })
      upstream.on('end', () => {
        const headers = { ...upstream.headers }
        delete headers['content-length']
        delete headers['content-encoding']
        response.writeHead(upstream.statusCode ?? 502, headers)
        response.end(marker ? body.replace('<head>', `<head><!-- ${marker} -->`) : body)
      })
    })
  } } })
  const origin = `http://localhost:${gateway.port}`
  const shellHtml = () => page.evaluate(async () => (await (await (await caches.open('flow-shell-v2')).match('/__flow-app-shell'))?.text()) ?? '')
  try {
    const account = await signUp(context, origin, 'worker-shell')
    const violations = await watchViolations(page)
    await page.goto(`${origin}/`)
    await expect.poll(() => page.evaluate(() => navigator.serviceWorker.controller?.state)).toBe('activated')
    // The first navigation the worker sees stores the shell together with every hashed file it references.
    await page.goto(`${origin}/`)
    await expect.poll(shellHtml, { timeout: 15_000 }).toContain('<div id="root">')
    const stored = await page.evaluate(async () => {
      const cache = await caches.open('flow-shell-v2')
      const html = await (await cache.match('/__flow-app-shell'))!.text()
      const assets = [...html.matchAll(/(?:src|href)="(\/assets\/[^"]+)"/g)].map(match => match[1])
      return { assets: assets.length, stored: (await Promise.all(assets.map(path => cache.match(path)))).filter(Boolean).length }
    })
    expect(stored.assets).toBeGreaterThan(0)
    expect(stored.stored).toBe(stored.assets)

    // A new deploy: the page still opens with the stored shell, the new one is stored meanwhile and used next time.
    marker = 'deploy-two'
    await page.goto(`${origin}/`)
    expect(await page.evaluate(() => document.documentElement.innerHTML.includes('deploy-two'))).toBe(false)
    await expect.poll(shellHtml, { timeout: 15_000 }).toContain('deploy-two')
    await page.goto(`${origin}/`)
    expect(await page.evaluate(() => document.documentElement.innerHTML.includes('deploy-two'))).toBe(true)
    await expect(page.locator('main').first()).toBeVisible()

    // Offline, an app page still opens from the shell; the API, sign-in pages and files are never answered by it.
    await context.setOffline(true)
    try {
      const shell = await page.goto(`${origin}/${account.workspaceKey}/inbox`)
      expect(shell?.status()).toBe(200)
      expect(shell?.fromServiceWorker()).toBe(true)
      for (const path of ['/api/health', '/login', '/auth/google/callback', '/oauth/authorize', '/favicon.svg']) {
        // Offline these fail, or come from the browser's own HTTP cache: never from the worker.
        const answer = await page.goto(`${origin}${path}`).catch(() => null)
        expect(answer?.fromServiceWorker() ?? false, path).toBe(false)
      }
    } finally {
      await context.setOffline(false)
    }

    // A shell whose files are gone is never served: the navigation goes to the network.
    await page.goto(`${origin}/`)
    await page.evaluate(async () => {
      const cache = await caches.open('flow-shell-v2')
      const html = await (await cache.match('/__flow-app-shell'))!.text()
      await cache.delete(html.match(/src="(\/assets\/[^"]+)"/)![1])
    })
    await context.setOffline(true)
    try {
      await expect(page.goto(`${origin}/${account.workspaceKey}/inbox`)).rejects.toThrow()
    } finally {
      await context.setOffline(false)
    }
    expect(violations.all()).toEqual([])
  } finally {
    await gateway.stop()
  }
})
