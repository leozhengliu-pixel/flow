import { defineConfig } from 'tsup'

export default defineConfig({
  entry: { index: 'src/index.ts', webhooks: 'src/webhooks.ts' },
  format: ['esm', 'cjs'],
  dts: true,
  sourcemap: true,
  clean: true,
  target: 'node18',
  platform: 'neutral',
  external: ['node:crypto'],
  removeNodeProtocol: false,
  treeshake: true,
  outExtension: ({ format }) => ({ js: format === 'cjs' ? '.cjs' : '.js' }),
})
