import postcss from 'postcss'
import { COMPACT_LAYOUT_QUERY, DESKTOP_LAYOUT_QUERY } from './src/lib/layout-breakpoints.ts'

/** Expand layout media aliases from the same queries used by React. */
export function layoutMedia() {
  return {
    name: 'flow-layout-media',
    // Tailwind's production transform resolves custom media before Vite's PostCSS pass.
    enforce: 'pre' as const,
    async transform(source: string, id: string) {
      if (!id.split('?')[0].endsWith('.css') || !source.includes('(--')) return
      const result = await postcss([{
        postcssPlugin: 'flow-layout-media',
        AtRule: {
          media(rule) {
            rule.params = rule.params.replaceAll('(--desktop-layout)', DESKTOP_LAYOUT_QUERY).replaceAll('(--compact-layout)', COMPACT_LAYOUT_QUERY)
          },
        },
      }]).process(source, { from: id, map: { inline: false, annotation: false } })
      return { code: result.css, map: result.map?.toJSON() }
    },
    transformIndexHtml(html: string) {
      return html.replaceAll('(--compact-layout)', COMPACT_LAYOUT_QUERY)
    },
  }
}
