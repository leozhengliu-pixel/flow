/** 16px filled glyphs of the PDF card toolbar and viewer (view, download, link and delete live in image-extension). */
export type GlyphPath = { d: string; evenodd?: boolean; transform?: string; stroke?: boolean }

export const PRINT_GLYPH: GlyphPath[] = [
  { d: 'M5 1.75C4.44772 1.75 4 2.19772 4 2.75V4.5H12V2.75C12 2.19772 11.5523 1.75 11 1.75H5Z' },
  { evenodd: true, d: 'M1.5 7.5C1.5 6.39543 2.39543 5.5 3.5 5.5H12.5C13.6046 5.5 14.5 6.39543 14.5 7.5V10.5C14.5 11.3284 13.8284 12 13 12H12V10.75C12 10.1977 11.5523 9.75 11 9.75H5C4.44772 9.75 4 10.1977 4 10.75V12H3C2.17157 12 1.5 11.3284 1.5 10.5V7.5ZM12.75 8.25C13.1642 8.25 13.5 7.91421 13.5 7.5C13.5 7.08579 13.1642 6.75 12.75 6.75C12.3358 6.75 12 7.08579 12 7.5C12 7.91421 12.3358 8.25 12.75 8.25Z' },
  { d: 'M5.25 11C5.11193 11 5 11.1119 5 11.25V13.5C5 14.0523 5.44772 14.5 6 14.5H10C10.5523 14.5 11 14.0523 11 13.5V11.25C11 11.1119 10.8881 11 10.75 11H5.25Z' },
]

export const ZOOM_OUT_GLYPH: GlyphPath[] = [
  { evenodd: true, d: 'M3.25 8C3.25 7.58579 3.58579 7.25 4 7.25H12C12.4142 7.25 12.75 7.58579 12.75 8C12.75 8.41421 12.4142 8.75 12 8.75H4C3.58579 8.75 3.25 8.41421 3.25 8Z' },
]

export const ZOOM_IN_GLYPH: GlyphPath[] = [
  { d: 'M8.75 4C8.75 3.58579 8.41421 3.25 8 3.25C7.58579 3.25 7.25 3.58579 7.25 4V7.25H4C3.58579 7.25 3.25 7.58579 3.25 8C3.25 8.41421 3.58579 8.75 4 8.75H7.25V12C7.25 12.4142 7.58579 12.75 8 12.75C8.41421 12.75 8.75 12.4142 8.75 12V8.75H12C12.4142 8.75 12.75 8.41421 12.75 8C12.75 7.58579 12.4142 7.25 12 7.25H8.75V4Z' },
]

const ARROW = 'M5.67382 11.4801C5.93899 11.7983 6.41191 11.8413 6.73012 11.5761C7.04833 11.311 7.09132 10.8381 6.82615 10.5198L5.35128 8.75H12.25C12.6642 8.75 13 8.41421 13 8C13 7.58579 12.6642 7.25 12.25 7.25H5.35125L6.82615 5.48012C7.09132 5.16191 7.04833 4.68899 6.73012 4.42382C6.41191 4.15865 5.93899 4.20164 5.67382 4.51985L3.17382 7.51985C2.94138 7.7988 2.94138 8.2012 3.17382 8.48015L5.67382 11.4801Z'
export const PREVIOUS_GLYPH: GlyphPath[] = [{ d: ARROW }]
export const NEXT_GLYPH: GlyphPath[] = [{ d: ARROW, transform: 'translate(16 0) scale(-1 1)' }]

export const CLOSE_GLYPH: GlyphPath[] = [{ d: 'M4.5 4.5L11.5 11.5M11.5 4.5L4.5 11.5', stroke: true }]

const SVG_NS = 'http://www.w3.org/2000/svg'

/** A 14px icon element for the vanilla (editor node view) toolbar. */
export function createGlyph(paths: GlyphPath[]) {
  const svg = document.createElementNS(SVG_NS, 'svg')
  svg.setAttribute('viewBox', '0 0 16 16')
  svg.setAttribute('width', '14')
  svg.setAttribute('height', '14')
  svg.setAttribute('fill', 'currentColor')
  svg.setAttribute('aria-hidden', 'true')
  for (const item of paths) {
    const path = document.createElementNS(SVG_NS, 'path')
    path.setAttribute('d', item.d)
    if (item.evenodd) { path.setAttribute('fill-rule', 'evenodd'); path.setAttribute('clip-rule', 'evenodd') }
    if (item.transform) path.setAttribute('transform', item.transform)
    if (item.stroke) { path.setAttribute('fill', 'none'); path.setAttribute('stroke', 'currentColor'); path.setAttribute('stroke-width', '1.5'); path.setAttribute('stroke-linecap', 'round') }
    svg.append(path)
  }
  return svg
}
