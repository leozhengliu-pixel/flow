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

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss(), inlineIconSprites()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, './src') } },
  build: {
    rolldownOptions: {
      output: {
        strictExecutionOrder: true,
        codeSplitting: {
          groups: [
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
