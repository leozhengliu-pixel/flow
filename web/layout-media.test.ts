import { expect, it } from 'vitest'
import { layoutMedia } from './layout-media'
import { COMPACT_LAYOUT_QUERY, DESKTOP_LAYOUT_QUERY } from './src/lib/layout-breakpoints'

it('expands CSS modules, combined queries and the initial HTML with the runtime boundary', async () => {
  const input = '@media (--desktop-layout) and (max-width: 1240px){.rail{display:flex}}@media (--compact-layout){.shell{display:none}}@media(max-width:767px){.phone{display:block}}'
  const { code: css } = (await layoutMedia().transform(input, 'surface.module.css'))!
  expect(css).toContain(`@media ${DESKTOP_LAYOUT_QUERY} and (max-width: 1240px)`)
  expect(css).toContain(`@media ${COMPACT_LAYOUT_QUERY}`)
  expect(css).toContain('@media(max-width:767px)')
  expect(css).not.toContain('--desktop-layout')
  expect(layoutMedia().transformIndexHtml('<style>@media (--compact-layout){.rail{display:none}}</style>')).toBe(`<style>@media ${COMPACT_LAYOUT_QUERY}{.rail{display:none}}</style>`)
})
