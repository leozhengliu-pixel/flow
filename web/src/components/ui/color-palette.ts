/**
 * The fixed colour palette every the reference app colour picker shows (labels, label groups, project and
 * issue statuses, customer statuses, release stages and the icon pickers), in picker order.
 * `value` is what gets stored; `token` names the CSS custom property the swatch is drawn with,
 * because the reference app draws some swatches with adjusted colours (purple and yellow always, the light
 * greys, orange and pink on light surfaces).
 */
export const FLOW_COLOR_PALETTE = [
  { name: 'Grey', value: '#bec2c8', token: '--color-swatch-grey' },
  { name: 'Dark Grey', value: '#95a2b3', token: '--color-swatch-dark-grey' },
  { name: 'Purple', value: '#5e6ad2', token: '--color-swatch-purple' },
  { name: 'Teal', value: '#26b5ce', token: '--color-swatch-teal' },
  { name: 'Green', value: '#4cb782', token: '--color-swatch-green' },
  { name: 'Yellow', value: '#f2c94c', token: '--color-swatch-yellow' },
  { name: 'Orange', value: '#f2994a', token: '--color-swatch-orange' },
  { name: 'Pink', value: '#f7c8c1', token: '--color-swatch-pink' },
  { name: 'Red', value: '#eb5757', token: '--color-swatch-red' },
] as const

export type FlowPaletteColor = (typeof FLOW_COLOR_PALETTE)[number]

/** The palette entry a stored colour matches (case-insensitive hex), if any. */
export function paletteColorFor(color: string): FlowPaletteColor | undefined {
  const normalized = color.trim().toLowerCase()
  return FLOW_COLOR_PALETTE.find(option => option.value === normalized)
}
