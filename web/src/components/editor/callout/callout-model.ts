/** The callout colour names saved by older documents; each resolves to a `--description-callout-*` token. */
export const calloutColors = ['cyan', 'gray', 'green', 'yellow', 'orange', 'red', 'purple'] as const
export type CalloutColor = typeof calloutColors[number]

/** The swatch the shared icon picker shows as selected for each legacy colour name (its preset hex values). */
const PICKER_HEX: Record<CalloutColor, string> = {
  cyan: '#24b4c7',
  gray: '#95a2b3',
  green: '#4cb782',
  yellow: '#f2c300',
  orange: '#eb9138',
  red: '#ee565d',
  purple: '#5e6ad2',
}

/** GitHub alert types ("> [!NOTE]") and the callout colour + emoji they become when Markdown is pasted. */
export const CALLOUT_ALERT_PRESETS: Record<'NOTE' | 'TIP' | 'IMPORTANT' | 'WARNING' | 'CAUTION', { color: CalloutColor; icon: string }> = {
  NOTE: { color: 'cyan', icon: 'ℹ️' },
  TIP: { color: 'green', icon: '💡' },
  IMPORTANT: { color: 'purple', icon: '❗' },
  WARNING: { color: 'orange', icon: '⚠️' },
  CAUTION: { color: 'red', icon: '🛑' },
}

const HEX = /^#[0-9a-f]{6}$/i

export function isCalloutColorName(value: unknown): value is CalloutColor {
  return typeof value === 'string' && (calloutColors as readonly string[]).includes(value)
}

/** A legacy colour name or a lowercase `#rrggbb`; anything else falls back to cyan. */
export function normalizeCalloutColor(value: unknown): string {
  if (isCalloutColorName(value)) return value
  if (typeof value === 'string' && HEX.test(value.trim())) return value.trim().toLowerCase()
  return 'cyan'
}

/** The hex the icon picker works in for a stored callout colour. */
export function calloutPickerColor(color: string): string {
  const normalized = normalizeCalloutColor(color)
  return isCalloutColorName(normalized) ? PICKER_HEX[normalized] : normalized
}

/** `:::callout{cyan icon="💡"}` attribute text (the part inside the braces). */
export function serializeCalloutAttrs(color: unknown, icon: unknown): string {
  const safeIcon = typeof icon === 'string' ? icon.replace(/["{}\n]/g, '') : ''
  return `${normalizeCalloutColor(color)}${safeIcon ? ` icon="${safeIcon}"` : ''}`
}

export function parseCalloutAttrs(source: string | undefined): { color: string; icon: string } {
  const text = source ?? ''
  const icon = /\bicon="([^"]*)"/.exec(text)?.[1] ?? ''
  const rest = text.replace(/\bicon="[^"]*"/, ' ').trim().split(/\s+/)[0]
  return { color: normalizeCalloutColor(rest), icon }
}
