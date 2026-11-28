import { defineConfig, devices } from '@playwright/test'
import base, { browserWebServers, desktopPerformanceSpecs } from './playwright.config.ts'

const ports = { web: 4575, api: 4582, provider: 4591 }

// The production bundle is built by npm locally or supplied by the CI browser
// build job before these servers start.
export default defineConfig({
  ...base,
  testIgnore: [],
  testMatch: desktopPerformanceSpecs,
  outputDir: 'test-results-performance',
  retries: 0,
  reporter: process.env.CI ? [['list'], ['github'], ['html', { open: 'never', outputFolder: 'playwright-report-performance' }]] : 'list',
  use: { ...base.use, baseURL: `http://127.0.0.1:${ports.web}` },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: browserWebServers(ports, 'preview'),
})
