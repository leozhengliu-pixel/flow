// Bundles the runner into one ESM file for the sandbox image
// (/opt/flow/runner/flow-runner.mjs). The runner has no runtime dependencies;
// track I's browser tools resolve playwright from the image at runtime.
import { build } from 'esbuild'

await build({
  entryPoints: ['src/main.ts'],
  outfile: 'dist/flow-runner.mjs',
  bundle: true,
  platform: 'node',
  target: 'node22',
  format: 'esm',
  sourcemap: 'linked',
  legalComments: 'none',
  external: ['playwright', 'playwright-core'],
  banner: {
    js: "import { createRequire as __flowCreateRequire } from 'node:module'; const require = __flowCreateRequire(import.meta.url);",
  },
})
