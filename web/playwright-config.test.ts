import { afterEach, expect, it, vi } from 'vitest'

afterEach(() => vi.unstubAllEnvs())

async function apiServer() {
  vi.resetModules()
  const { default: config } = await import('./playwright.config')
  const servers = Array.isArray(config.webServer) ? config.webServer : [config.webServer]
  return servers.find(server => server?.url === 'http://127.0.0.1:4180/api/health')!
}

it('keeps desktop specs in one project before browser sharding', async () => {
  const { default: config } = await import('./playwright.config')
  const desktop = config.projects!.find(project => project.name === 'chromium')!
  const mobile = config.projects!.find(project => project.name === 'mobile-chromium')!
  expect(desktop.testIgnore).not.toContain('**/desktop-*.spec.ts')
  expect(mobile.testIgnore).toContain('**/desktop-*.spec.ts')
})

it('runs the timeline performance spec only against a fresh production preview', async () => {
  const { default: dev, timelinePerformanceSpec } = await import('./playwright.config')
  const { default: performance } = await import('./playwright.performance.config')
  for (const project of dev.projects!) {
    expect(project.testIgnore).toContain(timelinePerformanceSpec)
  }
  expect(performance.testIgnore).not.toContain(timelinePerformanceSpec)
  expect(performance.testMatch).toBe(timelinePerformanceSpec)
  const servers = Array.isArray(performance.webServer) ? performance.webServer : [performance.webServer]
  expect(servers.find(server => server?.url === performance.use?.baseURL)?.command).toContain('npm run preview')
  for (const server of servers) expect(server?.reuseExistingServer).toBe(false)
})

it('retains the default browser API database path', async () => {
  vi.stubEnv('FLOW_E2E_DATABASE_PATH', undefined)
  expect((await apiServer()).env?.FLOW_E2E_DATABASE_PATH).toBe('/tmp/flow-e2e.db')
})

it('uses the same isolated database path for cleanup and the browser API', async () => {
  vi.stubEnv('FLOW_E2E_DATABASE_PATH', '/tmp/codex-browser-tests/flow.db')
  const server = await apiServer()
  expect(server.env?.FLOW_E2E_DATABASE_PATH).toBe('/tmp/codex-browser-tests/flow.db')
  expect(server.command).toContain('rm -f "$FLOW_E2E_DATABASE_PATH"')
  expect(server.command).toContain('FLOW_DATABASE_PATH="$FLOW_E2E_DATABASE_PATH"')
})
