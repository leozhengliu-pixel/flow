import { defineConfig, devices } from '@playwright/test'

/**
 * Two signed-in browsers on one workspace, against the production build the Go API serves with authentication on: an
 * editor's changes reach a viewer as realtime events applied in place, without reloading the workspace metadata. Each
 * spec starts its own API on a free port and a fresh database (e2e/security/harness.ts). The files end in .e2e.ts so
 * the main suite (auth disabled, one user) does not pick them up. Run: npm run test:e2e:realtime
 */
export default defineConfig({
  testDir: './e2e/realtime',
  testMatch: '**/*.e2e.ts',
  globalSetup: './e2e/realtime/global-setup.ts',
  timeout: 90_000,
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['github'], ['html', { open: 'never', outputFolder: 'playwright-report-realtime' }]] : 'list',
  outputDir: 'test-results-realtime',
  use: { trace: 'retain-on-failure', screenshot: 'only-on-failure' },
  projects: [{ name: 'realtime', use: { ...devices['Desktop Chrome'], channel: process.env.CI ? 'chromium' : 'chrome' } }],
})
