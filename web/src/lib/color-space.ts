const D50 = [0.96422, 1, 0.82521]

/** CIE LCH (D50) to XYZ (D65, Bradford-adapted). */
function lchToXyz(l: number, c: number, h: number): [number, number, number] {
  const a = c * Math.cos(h * Math.PI / 180), b = c * Math.sin(h * Math.PI / 180)
  const fy = (l + 16) / 116, fx = fy + a / 500, fz = fy - b / 200
  const e = 216 / 24389, k = 24389 / 27
  const xr = fx ** 3 > e ? fx ** 3 : (116 * fx - 16) / k
  const yr = l > k * e ? fy ** 3 : l / k
  const zr = fz ** 3 > e ? fz ** 3 : (116 * fz - 16) / k
  return multiply(D50_TO_D65, [xr * D50[0], yr * D50[1], zr * D50[2]])
}

type Matrix = readonly [readonly [number, number, number], readonly [number, number, number], readonly [number, number, number]]
const multiply = (m: Matrix, v: readonly number[]): [number, number, number] => [0, 1, 2].map(row => m[row][0] * v[0] + m[row][1] * v[1] + m[row][2] * v[2]) as [number, number, number]
const D50_TO_D65: Matrix = [[0.9554734527042182, -0.023098536874261423, 0.0632593086610217], [-0.028369706963208136, 1.0099954580106629, 0.021041398966943008], [0.012314001688319899, -0.020507696433477912, 1.3303659366080753]]
const XYZ_TO_P3: Matrix = [[2.4934969119, -0.9313836179, -0.4027107845], [-0.8294889696, 1.7626640603, 0.0236246858], [0.0358458302, -0.0761723893, 0.9568845240]]
const P3_TO_XYZ: Matrix = [[0.4865709486, 0.2656676932, 0.1982172852], [0.2289745641, 0.6917385218, 0.0792869141], [0, 0.0451133819, 1.0439443689]]
const XYZ_TO_SRGB: Matrix = [[3.2409699419, -1.5373831776, -0.4986107603], [-0.9692436363, 1.8759675015, 0.0415550574], [0.0556300797, -0.2039769589, 1.0569715142]]
const SRGB_TO_XYZ: Matrix = [[0.4123907993, 0.3575843394, 0.1804807884], [0.2126390059, 0.7151686788, 0.0721923054], [0.0193308187, 0.1191947798, 0.9505321522]]
const clip = (value: number) => Math.min(1, Math.max(0, value))

const encode = (value: number) => {
  const v = Math.min(1, Math.max(0, value))
  return v <= 0.0031308 ? v * 12.92 : 1.055 * v ** (1 / 2.4) - 0.055
}
const decode = (value: number) => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4

/**
 * CIE LCH to 8-bit sRGB the way the reference app paints its colour pad: the colour is clipped to the
 * display-p3 gamut, then drawn on an sRGB canvas (clipped again).
 */
export function lchToRgb(l: number, c: number, h: number): [number, number, number] {
  const p3 = multiply(XYZ_TO_P3, lchToXyz(l, c, h)).map(clip)
  return multiply(XYZ_TO_SRGB, multiply(P3_TO_XYZ, p3)).map(channel => Math.round(encode(channel) * 255)) as [number, number, number]
}

export function lchToHex(l: number, c: number, h: number): string {
  return `#${lchToRgb(l, c, h).map(channel => channel.toString(16).padStart(2, '0')).join('')}`
}

/** A `#rrggbb` colour as the `color(display-p3 …)` value the reference app paints the colour pad thumb with. */
export function hexToDisplayP3(hex: string): string {
  const linear = [1, 3, 5].map(offset => decode(Number.parseInt(hex.slice(offset, offset + 2), 16) / 255))
  const p3 = multiply(XYZ_TO_P3, multiply(SRGB_TO_XYZ, linear))
  return `color(display-p3 ${p3.map(channel => Number(encode(channel).toFixed(6))).join(' ')})`
}

/**
 * `#rrggbb` to CIE LCH (D50) with the CSS Color 4 matrices the reference app converts with. Greys keep the
 * tiny chroma those matrices leave, so their hue lands on 139.09°'s pad.
 */
export function hexToLch(hex: string): [number, number, number] | null {
  if (!/^#[0-9a-f]{6}$/i.test(hex)) return null
  const linear = [1, 3, 5].map(offset => decode(Number.parseInt(hex.slice(offset, offset + 2), 16) / 255))
  const xyz = multiply(D65_TO_D50, multiply(SRGB_TO_XYZ_EXACT, linear))
  const f = (t: number) => t > 216 / 24389 ? Math.cbrt(t) : (24389 / 27 * t + 16) / 116
  const [fx, fy, fz] = xyz.map((value, index) => f(value / D50_WHITE[index]))
  const l = 116 * fy - 16, a = 500 * (fx - fy), b = 200 * (fy - fz)
  const h = Math.atan2(b, a) * 180 / Math.PI
  return [l, Math.hypot(a, b), h < 0 ? h + 360 : h]
}

const SRGB_TO_XYZ_EXACT: Matrix = [[506752 / 1228815, 87881 / 245763, 12673 / 70218], [87098 / 409605, 175762 / 245763, 12673 / 175545], [7918 / 409605, 87881 / 737289, 1001167 / 1053270]]
const D65_TO_D50: Matrix = [[1.0479298208405488, 0.022946793341019088, -0.05019222954313557], [0.029627815688159344, 0.990434484573249, -0.01707382502938514], [-0.009243058152591178, 0.015055144896577895, 0.7518742899580008]]
const D50_WHITE = [0.3457 / 0.3585, 1, (1 - 0.3457 - 0.3585) / 0.3585]
