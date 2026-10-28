import { spawnSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

/**
 * Builds what the security specs run: the API binary and the production web build the binary serves. Set
 * FLOW_SECURITY_API_BIN and FLOW_SECURITY_WEB_DIST to reuse builds (CI can build once; locally, point them at earlier
 * builds to skip this).
 */
export default function globalSetup() {
  const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..')
  const cache = path.join(os.tmpdir(), 'flow-security-e2e')
  fs.mkdirSync(cache, { recursive: true })
  if (!process.env.FLOW_SECURITY_API_BIN) {
    const binary = path.join(cache, process.platform === 'win32' ? 'flow-api.exe' : 'flow-api')
    const built = spawnSync('go', ['build', '-o', binary, './cmd/server'], { cwd: path.join(web, '..', 'api'), stdio: 'inherit' })
    if (built.status !== 0) throw new Error('go build ./cmd/server failed')
    process.env.FLOW_SECURITY_API_BIN = binary
  }
  if (!process.env.FLOW_SECURITY_WEB_DIST) {
    const dist = path.join(cache, 'dist')
    const built = spawnSync('npx', ['vite', 'build', '--outDir', dist, '--emptyOutDir'], { cwd: web, stdio: 'inherit' })
    if (built.status !== 0) throw new Error('vite build failed')
    process.env.FLOW_SECURITY_WEB_DIST = dist
  }
}
