import { afterEach, expect, it, vi } from 'vitest'
import type { ConfigEnv, UserConfigFnPromise } from 'vite'

const factory = vi.hoisted(() => vi.fn(() => ({ name: 'test-agent-panel-seed' })))
vi.mock('./e2e/agent-panel-seed-plugin.ts', () => ({ agentPanelSeedPlugin: factory }))
import config from './vite.config'

afterEach(() => { vi.unstubAllEnvs(); factory.mockClear() })

const resolve = (command: ConfigEnv['command']) => (config as UserConfigFnPromise)({ command, mode: command === 'build' ? 'production' : 'development' })

it('does not import or instantiate the seed in ordinary dev', async () => {
  vi.stubEnv('FLOW_E2E_AGENT_PANEL_SEED', '')
  await resolve('serve')
  expect(factory).not.toHaveBeenCalled()
})

it('does not instantiate or read the seed in production, even with its flag set', async () => {
  vi.stubEnv('FLOW_E2E_AGENT_PANEL_SEED', '1')
  await resolve('build')
  expect(factory).not.toHaveBeenCalled()
})

it('loads the seed only for an explicitly enabled dev server', async () => {
  vi.stubEnv('FLOW_E2E_AGENT_PANEL_SEED', '1')
  await resolve('serve')
  expect(factory).toHaveBeenCalledOnce()
})
