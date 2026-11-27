import type { ResolvedTheme } from './theme'

/**
 * Label colours as the design reference draws them. Colours are CIE LCH (D50) triples, converted, mixed and
 * contrast-checked with the same arithmetic as the reference's colour engine, so dots and group header tints land
 * on the same values:
 *
 * - A label dot keeps its colour when it reads against the page (APCA contrast above 18 in light, 38 in dark), and
 *   otherwise moves its lightness by bisection until it does; a few palette hexes map to the theme's own colours.
 * - A label group header mixes the page colour with the label's hue at full lightness (2% in light, 0.2% in dark,
 *   in XYZ D50); the header's gradient starts at that mix and its muted text takes the mix's hue.
 */

type Lch = [number, number, number]

interface LabelColorTheme {
  /** The page background the dot contrast is measured against. */
  page: Lch
  /** The background a group header mixes its tint into. */
  header: Lch
  /** A board row header's background. */
  row: Lch
  contrast: number
  tintStrength: number
  palette: Record<string, Lch>
}

const PALETTE_LIGHT: Record<string, Lch> = { '#e8b600': [80, 90, 85], '#f2c94c': [80, 90, 85], '#5e6ad2': [48, 59.31, 288.43], '#fc7840': [66, 80, 48], '#0f783c': [60, 64.37, 141.95] }
const PALETTE_DARK: Record<string, Lch> = { ...PALETTE_LIGHT, '#0f783c': [68, 64.37, 141.95] }

const THEMES: Record<ResolvedTheme, LabelColorTheme> = {
  light: { page: [97.94, 0.5, 282], header: [94.44, 0.5, 282], row: [94.44, 0.5, 282], contrast: 18, tintStrength: 0.02, palette: PALETTE_LIGHT },
  dark: { page: [5.52, 0.4, 272], header: [9.232, 0.85, 272], row: [7.32, 0.85, 272], contrast: 38, tintStrength: 0.002, palette: PALETTE_DARK },
}

const D50: Lch = [0.3457 / 0.3585, 1, 0.2958 / 0.3585]
const SRGB_TO_XYZ_D65 = [[0.41239079926595934, 0.357584339383878, 0.1804807884018343], [0.21263900587151027, 0.715168678767756, 0.07219231536073371], [0.01933081871559182, 0.11919477979462598, 0.9505321522496607]]
const D65_TO_D50 = [[1.0479298208405488, 0.022946793341019088, -0.05019222954313557], [0.029627815688159344, 0.990434484573249, -0.01707382502938514], [-0.009243058152591178, 0.015055144896577895, 0.7518742899580008]]

function multiply(matrix: number[][], vector: number[]): Lch {
  return [0, 1, 2].map(row => matrix[row][0] * vector[0] + matrix[row][1] * vector[1] + matrix[row][2] * vector[2]) as Lch
}
const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value))

function linearize(channel: number) {
  const sign = channel < 0 ? -1 : 1, magnitude = Math.abs(channel)
  return magnitude < 0.04045 ? channel / 12.92 : sign * ((magnitude + 0.055) / 1.055) ** 2.4
}

function xyzToLab(xyz: Lch): Lch {
  const f = xyz.map((value, index) => value / D50[index]).map(value => value > 0.008856451679035631 ? Math.cbrt(value) : (903.2962962962963 * value + 16) / 116)
  return [116 * f[1] - 16, 500 * (f[0] - f[1]), 200 * (f[1] - f[2])]
}

function labToXyz(lab: Lch): Lch {
  const kappa = 24389 / 27, epsilon = 216 / 24389
  const fy = (lab[0] + 16) / 116, fx = lab[1] / 500 + fy, fz = fy - lab[2] / 200
  return [fx ** 3 > epsilon ? fx ** 3 : (116 * fx - 16) / kappa, lab[0] > kappa * epsilon ? fy ** 3 : lab[0] / kappa, fz ** 3 > epsilon ? fz ** 3 : (116 * fz - 16) / kappa].map((value, index) => value * D50[index]) as Lch
}

function labToLch(lab: Lch): Lch {
  const hue = Math.atan2(lab[2], lab[1]) * 180 / Math.PI
  return [lab[0], Math.hypot(lab[1], lab[2]), hue >= 0 ? hue : hue + 360]
}

function lchToLab(lch: Lch): Lch {
  return [lch[0], lch[1] * Math.cos(lch[2] * Math.PI / 180), lch[1] * Math.sin(lch[2] * Math.PI / 180)]
}

/** A `#rgb` / `#rrggbb` colour as CIE LCH (D50); undefined for anything else. */
export function hexToLch(color: string): Lch | undefined {
  const match = /^#?([\da-f]{3}|[\da-f]{6})(?:[\da-f]{2})?$/i.exec(color.trim())
  if (!match) return undefined
  const hex = match[1].length === 3 ? match[1].split('').map(digit => digit + digit).join('') : match[1]
  const rgb = [0, 2, 4].map(offset => linearize(parseInt(hex.slice(offset, offset + 2), 16) / 255))
  return labToLch(xyzToLab(multiply(D65_TO_D50, multiply(SRGB_TO_XYZ_D65, rgb))))
}

/** Mixes two LCH colours linearly in XYZ (D50), `amount` of the second. */
export function mixLch(from: Lch, to: Lch, amount: number): Lch {
  const a = labToXyz(lchToLab(from)), b = labToXyz(lchToLab(to))
  const mixed = labToLch(xyzToLab([0, 1, 2].map(index => a[index] * (1 - amount) + b[index] * amount) as Lch))
  return [clamp(mixed[0], 0, 100), clamp(mixed[1], 0, 132), clamp(mixed[2], 0, 360)]
}

const softClamp = (value: number) => value >= 0.022 ? value : value + (0.022 - value) ** 1.414

/** The APCA-style lightness contrast of text `text` on `background` (both LCH). */
export function lightnessContrast(text: Lch, background: Lch) {
  const foreground = softClamp(text[0] / 100), back = softClamp(background[0] / 100)
  let raw: number
  if (Math.abs(back - foreground) < 0.0005) raw = 0
  else if (back > foreground) raw = (back ** 0.56 - foreground ** 0.57) * 1.14
  else raw = (back ** 0.65 - foreground ** 0.62) * 1.14
  const scaled = Math.abs(raw) < 0.1 ? 0 : raw > 0 ? raw - 0.027 : raw + 0.027
  return Math.abs(scaled * 100)
}

/** The colour's lightness moved, by bisection toward the background's opposite end, until it reaches `contrast`. */
function contrastingLightness(color: Lch, background: Lch, contrast: number): Lch | undefined {
  let low = 0, high = 100, found = high, ok = false
  while (high - low > 1) {
    const middle = (low + high) / 2
    if (lightnessContrast([middle, color[1], color[2]], background) > contrast) {
      ok = true
      found = middle
      if (background[0] > middle) low = middle
      else high = middle
    } else if (background[0] > middle) high = middle
    else low = middle
  }
  return ok ? [found, color[1], color[2]] : undefined
}

const round3 = (value: number) => parseFloat(value.toFixed(3))
export function lchCss([l, c, h]: Lch) { return `lch(${round3(l)}% ${round3(c)} ${round3(h)})` }

const displayCache = new Map<string, string>()

/** A label's colour as its dot shows it in `theme`: the label colour when it reads on the page, else a contrasting lightness. */
export function labelDisplayColor(color: string, theme: ResolvedTheme): string {
  const key = `${theme}:${color}`
  const cached = displayCache.get(key)
  if (cached) return cached
  const settings = THEMES[theme]
  const palette = settings.palette[color.trim().toLowerCase()]
  const lch = palette ?? hexToLch(color)
  let result = color
  if (palette) result = lchCss(palette)
  else if (lch && lightnessContrast(lch, settings.page) <= settings.contrast) result = lchCss(contrastingLightness(lch, settings.page, settings.contrast) ?? lch)
  displayCache.set(key, result)
  return result
}

/**
 * The tint a label group header is drawn with in `theme`: the header background mixed with the label's hue at full
 * lightness (full chroma for colourful labels). The gradient starts here and the header's muted text takes its hue.
 */
export function labelHeaderTint(color: string, theme: ResolvedTheme, surface: 'header' | 'page' | 'row' = 'header'): string | undefined {
  const lch = hexToLch(color)
  if (!lch) return undefined
  const settings = THEMES[theme]
  // Group headers sit on the raised header surface, board row headers on their own; row text tints against the page.
  return lchCss(mixLch(settings[surface], [100, lch[1] > 11 ? 100 : lch[1], lch[2]], settings.tintStrength))
}
