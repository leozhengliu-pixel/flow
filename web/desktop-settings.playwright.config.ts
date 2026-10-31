import { defineConfig } from '@playwright/test'
export default defineConfig({ testDir: './e2e', testMatch: 'desktop-settings-parity.spec.ts', workers: 1, reporter: 'list', outputDir: '/tmp/codex-desktop-settings/e2e', use: { baseURL: 'http://127.0.0.1:5464', viewport: { width: 1440, height: 900 }, browserName: 'chromium', screenshot: 'off', trace: 'off' } })
