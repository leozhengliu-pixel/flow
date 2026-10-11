export function projectStatusOptionColor(status: { id: string; name: string; color: string }, current: { id: string; name: string; color: string }) {
  return status.id === current.id || status.name === current.name ? current.color : status.color
}

/** The reference app's project status colour swatches, in picker order. */
export const PROJECT_STATUS_SWATCHES: ReadonlyArray<{ name: string; color: string }> = [
  { name: 'Grey', color: '#bec2c8' },
  { name: 'Dark Grey', color: '#95a2b3' },
  { name: 'Purple', color: '#5e6ad2' },
  { name: 'Teal', color: '#26b5ce' },
  { name: 'Green', color: '#4cb782' },
  { name: 'Yellow', color: '#f2c94c' },
  { name: 'Orange', color: '#f2994a' },
  { name: 'Pink', color: '#f7c8c1' },
  { name: 'Red', color: '#eb5757' },
]

/** Default colour per status type for a newly created status. */
export const PROJECT_STATUS_DEFAULT_COLORS: Record<string, string> = {
  backlog: '#f2994a',
  planned: '#d7d8db',
  started: '#f2c94c',
  completed: '#5e6ad2',
  canceled: '#8a8f98',
}

/** Highest lightness a hex status colour keeps on a light surface; brighter colours are darkened to it. */
const LIGHT_SURFACE_MAX_LIGHTNESS = 70.313
/** The reference app draws its brand yellow and purple with fixed LCH colours instead of the stored hex. */
const STROKE_OVERRIDES: Record<string, string> = { '#f2c94c': 'lch(80% 90 85)', '#5e6ad2': 'lch(48% 59.31 288.43)' }

export function normalizeHexColor(value: string): string | null {
  const trimmed = value.trim()
  const short = /^#?([0-9a-f]{3})$/i.exec(trimmed)
  if (short) return `#${short[1].split('').map(part => part + part).join('').toLowerCase()}`
  const full = /^#?([0-9a-f]{6})$/i.exec(trimmed)
  return full ? `#${full[1].toLowerCase()}` : null
}

/** sRGB hex to CIE LCH (D50), the space the reference app writes its adjusted colours in. */
export function hexToLch(value: string): [number, number, number] | null {
  const hex = normalizeHexColor(value)
  if (!hex) return null
  const channel = (offset: number) => {
    const v = Number.parseInt(hex.slice(offset, offset + 2), 16) / 255
    return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4
  }
  const r = channel(1), g = channel(3), b = channel(5)
  // The reference app sRGB -> XYZ (D50, Bradford-adapted).
  const x = 0.4360747 * r + 0.3850649 * g + 0.1430804 * b
  const y = 0.2225045 * r + 0.7168786 * g + 0.0606169 * b
  const z = 0.0139322 * r + 0.0971045 * g + 0.7141733 * b
  const white = [0.96422, 1, 0.82521]
  const f = (t: number) => (t > 216 / 24389 ? Math.cbrt(t) : (24389 / 27 * t + 16) / 116)
  const fx = f(x / white[0]), fy = f(y / white[1]), fz = f(z / white[2])
  const L = 116 * fy - 16, A = 500 * (fx - fy), B = 200 * (fy - fz)
  const C = Math.hypot(A, B)
  let H = Math.atan2(B, A) * 180 / Math.PI
  if (H < 0) H += 360
  return [L, C, H]
}

const round = (value: number) => Math.round(value * 1000) / 1000

/**
 * The colour a project status icon strokes with. On light surfaces the reference app darkens bright hex
 * colours to keep contrast; colours already written in LCH (and dark surfaces) are kept as-is.
 */
export function projectStatusStrokeColor(color: string, theme: 'light' | 'dark'): string {
  const hex = normalizeHexColor(color)
  if (!hex) return color
  if (STROKE_OVERRIDES[hex]) return STROKE_OVERRIDES[hex]
  if (theme === 'dark') return color
  const lch = hexToLch(hex)
  if (!lch || lch[0] <= LIGHT_SURFACE_MAX_LIGHTNESS) return color
  return `lch(${LIGHT_SURFACE_MAX_LIGHTNESS}% ${round(lch[1])} ${round(lch[2])})`
}
