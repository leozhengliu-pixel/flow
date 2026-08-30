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
      reporter: ['text', 'json-summary', 'lcov'],
      include: [
        'src/lib/group-options.ts',
        'src/lib/labels.ts',
        'src/lib/project-status-color.ts',
        'src/lib/resource-counts.ts',
        'src/components/cycles/cycle-model.ts',
        'src/components/pulse/pulse-model.ts',
        'src/components/projects-page/projects-filter-model.ts',
        'src/components/ui/action-dialog-service.ts',
        'src/components/ui/action-dialogs.tsx',
      ],
      thresholds: { branches: 80, functions: 80, lines: 80, statements: 80 },
    },
  },
})
