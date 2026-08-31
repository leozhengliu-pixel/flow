import path from 'node:path'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [react()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, './src') } },
  test: {
    environment: 'jsdom',
    exclude: ['e2e/**', 'node_modules/**', 'dist/**'],
    setupFiles: ['./src/test/setup.ts'],
    coverage: {
      provider: 'v8',
      reportsDirectory: './coverage/workflows',
      reporter: ['text', 'json-summary', 'lcov'],
      include: [
        'src/components/agent/agent-page.tsx',
        'src/components/agent/agent-chat-panel.tsx',
        'src/components/agent/agent-stream-state.ts',
        'src/components/issue-explorer/issue-explorer-model.ts',
        'src/components/project-detail/project-overview.tsx',
        'src/components/project-detail/project-detail-helpers.ts',
        'src/components/project-detail/project-issue-display.ts',
        'src/components/inbox/inbox-controller.ts',
        'src/components/inbox/inbox-filter-types.ts',
        'src/components/inbox/inbox-list-state.tsx',
        'src/components/settings/settings-primitives.tsx',
        'src/components/settings/personal-settings.tsx',
        'src/components/property/use-property-command.ts',
        'src/components/issue/issue-subscriber-picker.tsx',
        'src/components/issue/editor/use-issue-autosave.ts',
        'src/lib/api-client.ts',
        'src/lib/agent-stream.ts',
        'src/lib/issue-collaboration.ts',
      ],
      thresholds: { branches: 40, functions: 50, lines: 70, statements: 60 },
    },
  },
})
