import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  testMatch: ['resource-window-enable.spec.ts', 'resource-window-read-write.spec.ts'],
  timeout: 90_000,
  workers: 1,
  retries: 0,
  outputDir: '/tmp/codex-scale-p20-enable/playwright',
  reporter: 'list',
  use: { ...devices['Desktop Chrome'], trace: 'retain-on-failure' },
})
