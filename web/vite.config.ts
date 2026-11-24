import { defineConfig, type HtmlTagDescriptor, type Plugin, type UserConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'
import { readFileSync } from 'node:fs'
import { tourMedia } from './tour-media-plugin.ts'
import { releaseManifest } from './release-assets.ts'
import { layoutMedia } from './layout-media.ts'

const apiProxyTarget = process.env.VITE_API_PROXY_TARGET ?? 'http://localhost:8080'

function preloadAppEntry(): Plugin {
  const entry = path.resolve(import.meta.dirname, 'src/app-entry.tsx')
  return {
    name: 'flow-preload-app-entry',
    apply: 'build',
    transformIndexHtml(html, { bundle }) {
      const app = Object.values(bundle ?? {}).find(output => output.type === 'chunk' && output.moduleIds.includes(entry))
      if (!app || !bundle) return
      const seen = new Set<string>()
      const tags: HtmlTagDescriptor[] = []
      const add = (file: string, attrs: HtmlTagDescriptor['attrs']) => {
        if (seen.has(file) || html.includes(`"/${file}"`)) return
        seen.add(file)
        tags.push({ tag: 'link', attrs: { ...attrs, href: `/${file}` }, injectTo: 'head' })
      }
      const pending = [app.fileName]
      const modules = new Set<string>()
      for (const file of pending) {
        if (modules.has(file)) continue
        modules.add(file)
        const chunk = bundle[file]
        if (chunk?.type !== 'chunk') continue
        add(file, { rel: 'modulepreload', crossorigin: true })
        for (const css of chunk.viteMetadata?.importedCss ?? []) add(css, { rel: 'stylesheet', crossorigin: true })
        pending.push(...chunk.imports)
      }
      return tags
    },
  }
}

function asyncStartupEntry(): Plugin {
  return {
    name: 'flow-async-startup-entry',
    apply: 'build',
    transformIndexHtml: {
      order: 'pre',
      // Start the authority reads without waiting for the app's stylesheets.
      handler: html => html.replace('src="/src/local-store-entry.ts"', 'async src="/src/local-store-entry.ts"'),
    },
  }
}

function inlineIconSprites() {
  return {
    name: 'flow-inline-icon-sprites',
    transformIndexHtml(html: string) {
      const publicDir = path.resolve(import.meta.dirname, 'public')
      const sprites = ['flow-core-icons.svg', 'flow-property-icons.svg', 'flow-milestone-icons.svg'].map(file => readFileSync(path.join(publicDir, file), 'utf8').replace(/^<svg\b([^>]*)>/, (_match, attributes: string) => `<svg aria-hidden="true" focusable="false" width="0" height="0" style="position:absolute;width:0;height:0;overflow:hidden"${attributes.replace(/\s(?:width|height)="[^"]*"/g, '')}>`)).join('')
      return html.replace('<body>', `<body>${sprites}`)
    },
  }
}

// Radix Popper imports useFloating from '@floating-ui/react-dom'; the app's copy (src/lib/floating-ui-react-dom.ts) puts desktop
// popups on whole pixels. Only that module may reach the real package.
const FLOATING_SNAP_SHIM = path.resolve(import.meta.dirname, 'src/lib/floating-ui-react-dom.ts')

function snapFloatingPositions() {
  return {
    name: 'flow-snap-floating-positions',
    enforce: 'pre' as const,
    resolveId(source: string, importer?: string) {
      if (source !== '@floating-ui/react-dom' || !importer || path.resolve(importer.split('?')[0]) === FLOATING_SNAP_SHIM) return null
      return FLOATING_SNAP_SHIM
    },
  }
}

// elkjs (1.4 MB) and @mermaid-js/parser (660 kB) ship as one pre-bundled file each, so they cannot be split any further.
// Both are fetched only when a mermaid diagram renders. `chunkSizeWarningLimit` below is raised to fit them, so this
// plugin keeps Vite's usual 500 kB warning for every other chunk.
const CHUNK_WARNING_BYTES = 500_000
const UNSPLITTABLE_VENDOR = /node_modules[\\/](?:elkjs|@mermaid-js[\\/]parser)[\\/]/

function chunkSizeBudget() {
  return {
    name: 'flow-chunk-size-budget',
    generateBundle(_options: unknown, bundle: Record<string, { type: string; fileName: string; code?: string; moduleIds?: string[] }>) {
      for (const chunk of Object.values(bundle)) {
        if (chunk.type !== 'chunk' || Buffer.byteLength(chunk.code ?? '') <= CHUNK_WARNING_BYTES) continue
        if ((chunk.moduleIds ?? []).every(id => id.startsWith('\0') || UNSPLITTABLE_VENDOR.test(id))) continue
        ;(this as unknown as { warn(message: string): void }).warn(`Chunk ${chunk.fileName} is larger than ${CHUNK_WARNING_BYTES / 1000} kB; split it with build.rolldownOptions.output.codeSplitting or lazy-load it.`)
      }
    },
  }
}

// https://vite.dev/config/
export default defineConfig(async ({ command }): Promise<UserConfig> => ({
  // tourMedia: docs/media/*.gif → /tour/ (dev server and build output) for the Getting started issues.
  // releaseManifest: dist/asset-manifest.json, which lets other releases' servers find this build's assets.
  plugins: [layoutMedia(), snapFloatingPositions(), react(), tailwindcss(), inlineIconSprites(),
    asyncStartupEntry(), preloadAppEntry(),
    command === 'serve' && process.env.FLOW_E2E_AGENT_PANEL_SEED === '1'
      ? (await import('./e2e/agent-panel-seed-plugin.ts')).agentPanelSeedPlugin(apiProxyTarget)
      : undefined,
    chunkSizeBudget(), tourMedia(), releaseManifest()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, './src') } },
  // Radix is pre-bundled in dev with its own copy of floating-ui; route that copy through the same module.
  optimizeDeps: { rolldownOptions: { plugins: [snapFloatingPositions()] } },
  build: {
    // Small KaTeX fonts must stay same-origin files under font-src 'self', not data URLs.
    assetsInlineLimit: (file: string) => /\.(?:woff2?|ttf|otf|eot)$/i.test(file) ? false : undefined,
    chunkSizeWarningLimit: 1500,
    rolldownOptions: {
      output: {
        strictExecutionOrder: true,
        // The retained view and its authority request start alongside the route chunks.
        // Group startup dependencies to avoid a long chain of app-shell cache lookups.
        codeSplitting: {
          groups: [
            { name: 'startup', tags: ['$initial'], priority: 1, maxSize: 420_000 },
            { name: 'app-runtime', test: /[\\/]src[\\/](?:app-entry\.tsx|components[\\/](?:issue-explorer[\\/]issue-explorer-page|my-issues[\\/]my-issues-page)\.tsx)$/, maxSize: 900_000 },
          ],
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: { '/api': { target: apiProxyTarget, ws: true }, '/uploads': apiProxyTarget, '/scim': apiProxyTarget },
  },
}))
