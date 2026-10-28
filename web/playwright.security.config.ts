import { defineConfig, devices } from '@playwright/test'

/**
 * Browser checks of the response security headers (Content Security Policy, Permissions-Policy) against the production
 * build served by the Go API: passkeys, embeds, math, the collaboration socket, CSP reports and the service worker.
 * Each spec starts the API it needs (e2e/security/harness.ts), so this config has no webServer. Run: npm run test:e2e:security
 */
export default defineConfig({
  testDir: './e2e/security',
  testMatch: '**/*.spec.ts',
  globalSetup: './e2e/security/global-setup.ts',
  timeout: 60_000,
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['github'], ['html', { open: 'never', outputFolder: 'playwright-report-security' }]] : 'list',
  outputDir: 'test-results-security',
  use: {
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    // The report tests run an HTTPS gateway with a throwaway self-signed certificate (harness.ts).
    ignoreHTTPSErrors: true,
  },
  projects: [
    {
      name: 'security',
      use: {
        ...devices['Desktop Chrome'],
        // Locally the system Chrome; in CI Playwright's bundled Chromium in its new headless mode ('chromium' channel). The
        // default headless shell build never delivers Reporting API reports (Reporting-Endpoints / report-to), which the CSP
        // report test needs; the new headless mode is the full browser and does.
        channel: process.env.CI ? 'chromium' : 'chrome',
        // Reporting API uploads are made by the browser's network service, which ignoreHTTPSErrors does not reach.
        launchOptions: { args: ['--ignore-certificate-errors'] },
      },
    },
  ],
})
