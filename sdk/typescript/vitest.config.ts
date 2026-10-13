import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    include: ['test/**/*.test.ts'],
    environment: 'node',
  },
  resolve: {
    alias: {
      '@flow/sdk/webhooks': new URL('./src/webhooks.ts', import.meta.url).pathname,
      '@flow/sdk': new URL('./src/index.ts', import.meta.url).pathname,
    },
  },
})
