import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'
import { readFileSync } from 'node:fs'

const apiProxyTarget = process.env.VITE_API_PROXY_TARGET ?? 'http://localhost:8080'

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
export default defineConfig({
  plugins: [react(), tailwindcss(), inlineIconSprites(), chunkSizeBudget()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, './src') } },
  build: {
    chunkSizeWarningLimit: 1500,
    rolldownOptions: {
      output: {
        strictExecutionOrder: true,
        codeSplitting: {
          groups: [
            // Diagram rendering is loaded on demand (see mermaid-preview.ts); keep its libraries out of the shared vendor chunks.
            { name: 'diagram-vendor', test: /node_modules[\\/](?:mermaid|@mermaid-js|elkjs|cytoscape[^\\/]*|dagre-d3-es|langium|chevrotain[^\\/]*|@chevrotain|katex|roughjs|khroma|stylis|marked)[\\/]/, priority: 40, maxSize: 250_000 },
            { name: 'react-vendor', test: /node_modules[\\/](?:react|react-dom|react-router|react-router-dom|scheduler)[\\/]/, priority: 30 },
            { name: 'editor-vendor', test: /node_modules[\\/](?:@tiptap|prosemirror|yjs|y-prosemirror|lib0|markdown-it)[\\/]/, priority: 25, maxSize: 250_000 },
            { name: 'ui-vendor', test: /node_modules[\\/](?:@radix-ui|cmdk|lucide-react|sonner)[\\/]/, priority: 20, maxSize: 220_000 },
            { name: 'chart-vendor', test: /node_modules[\\/](?:@nivo|d3-)[\\/]/, priority: 15, maxSize: 250_000 },
            { name: 'date-vendor', test: /node_modules[\\/](?:date-fns|chrono-node)[\\/]/, priority: 10, maxSize: 220_000 },
            { name: 'vendor', test: /node_modules[\\/]/, priority: 1, maxSize: 250_000 },
          ],
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: { '/api': { target: apiProxyTarget, ws: true }, '/uploads': apiProxyTarget },
  },
})
