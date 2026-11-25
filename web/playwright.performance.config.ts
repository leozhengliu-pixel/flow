import { defineConfig, devices } from '@playwright/test'
import base, { browserWebServers, timelinePerformanceSpec } from './playwright.config.ts'

const ports = { web: 4575, api: 4582, provider: 4591 }

// npm run test:e2e:performance builds the bundle before starting these servers.
export default defineConfig({
  ...base,
  testIgnore: [],
  testMatch: timelinePerformanceSpec,
  outputDir: 'test-results-performance',
  retries: 0,
  reporter: process.env.CI ? [['list'], ['github'], ['html', { open: 'never', outputFolder: 'playwright-report-performance' }]] : 'list',
  use: { ...base.use, baseURL: `http://127.0.0.1:${ports.web}` },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: browserWebServers(ports, 'preview'),
})
