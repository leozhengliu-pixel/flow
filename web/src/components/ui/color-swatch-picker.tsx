import { useEffect, useRef, useState, type CSSProperties, type KeyboardEvent, type PointerEvent } from 'react'
import { hexToDisplayP3, hexToLch, lchToHex, lchToRgb } from '@/lib/color-space'
import { useI18n } from '@/i18n/i18n'
import { FLOW_COLOR_PALETTE, paletteColorFor } from './color-palette'
import { FlowGlyph } from './menu-glyphs'
import './color-swatch-picker.css'

/**
 * Which the reference app surface the picker sits on. The reference app tints its pickers by elevation: a popover over
 * the page (labels, project icon), a popover over a settings card (statuses), or the 16.4%
 * surface the icon picker uses over a settings card (team, view, initiative icons).
 */
export type ColorSwatchSurface = 'popover' | 'elevated' | 'icon'
export type ColorChangeSource = 'swatch' | 'custom'

/**
 * The reference app's colour picker: a fixed row of nine swatches (the selected one carries the ✓) with
 * a rainbow toggle, or — when the colour is not one of the swatches, or the toggle is pressed —
 * the current colour, a HEX field and a saturation / hue pad. Shared by every Flow colour picker.
 */
export function ColorSwatchPicker({ className, color, framed = false, note, onChange, onPreview, surface = 'popover' }: {
  className?: string
  color: string
  /** Draws the popover border and radius (standalone popovers); the icon picker embeds it unframed. */
  framed?: boolean
  /** Text after the HEX field, e.g. "Contrast has been adjusted" for status colours. */
  note?: string
  /** Commits a colour: a swatch click, a HEX entry, or the end of a saturation / hue drag. */
  onChange: (color: string, source: ColorChangeSource) => void
  /** Live colour while dragging the saturation / hue pad or typing a valid HEX value. */
  onPreview?: (color: string) => void
  surface?: ColorSwatchSurface
}) {
  const { t } = useI18n()
  const current = normalizeHex(color)
  const [custom, setCustom] = useState(() => !paletteColorFor(current ?? ''))
  return <div className={`csp${className ? ` ${className}` : ''}`} data-framed={framed || undefined} data-mode={custom ? 'custom' : 'swatches'} data-surface={surface}>
    {custom
      ? <CustomColorPanel color={current ?? DEFAULT_CUSTOM_COLOR} note={note} onChange={next => onChange(next, 'custom')} onPreview={onPreview}/>
      : <div aria-label={t('Color')} className="csp-swatches" role="radiogroup">
        {FLOW_COLOR_PALETTE.map(swatch => {
          const selected = current === swatch.value
          return <button aria-checked={selected} aria-label={t(swatch.name)} className="csp-swatch" data-color={swatch.value} key={swatch.value} onClick={() => onChange(swatch.value, 'swatch')} role="radio" style={{ '--palette-color': `var(${swatch.token})` } as CSSProperties} type="button">
            {selected && <FlowGlyph height={9} name="swatchCheck" width={10}/>}
          </button>
        })}
      </div>}
    <div className="csp-toggle-wrap">
      <button aria-label={t(custom ? 'Select default color' : 'Set custom color')} aria-pressed={custom} className="csp-toggle" data-custom-color={!paletteColorFor(current ?? '') || undefined} onClick={() => setCustom(value => !value)} type="button"><span aria-hidden="true"/></button>
    </div>
  </div>
}

const DEFAULT_CUSTOM_COLOR = '#95a2b3'
/** The reference app's pad spans CIE LCH chroma 0–132 left to right and lightness 100–0 top to bottom. */
const MAX_CHROMA = 132
/** Lightness and chroma the reference app paints the hue strip at. */
const HUE_STRIP_LIGHTNESS = 75
const HUE_STRIP_CHROMA = 132

type Lch = { l: number; c: number; h: number }

function lchOf(hex: string, fallbackHue: number): Lch {
  const [l, c, h] = hexToLch(hex) ?? [0, 0, fallbackHue]
  return { l, c, h }
}

function CustomColorPanel({ color, note, onChange, onPreview }: { color: string; note?: string; onChange: (color: string) => void; onPreview?: (color: string) => void }) {
  const { t } = useI18n()
  const padRef = useRef<HTMLCanvasElement>(null)
  const hueRef = useRef<HTMLCanvasElement>(null)
  // The colour being dragged or typed (null shows the committed colour), and where its thumbs sit.
  const [live, setLive] = useState<{ hex: string; lch: Lch } | null>(null)
  const [draft, setDraft] = useState(color)
  const [synced, setSynced] = useState(color)
  const [hue, setHue] = useState(() => lchOf(color, 0).h)
  if (synced !== color) {
    setSynced(color)
    setDraft(color)
    if (!live) setHue(lchOf(color, hue).h)
  }
  const shown = live?.hex ?? color
  const position = live?.lch ?? lchOf(color, hue)
  // The committed colour when a drag or HEX edit began; previews may feed back into `color`.
  const baseline = useRef<string | null>(null)
  const preview = (hex: string, lch: Lch) => {
    baseline.current ??= color
    setLive({ hex, lch })
    setHue(lch.h)
    setDraft(hex)
    onPreview?.(hex)
  }
  const commit = (hex: string, lch: Lch) => {
    const before = baseline.current ?? color
    baseline.current = null
    setLive(null)
    setHue(lch.h)
    setDraft(hex)
    if (hex !== before) onChange(hex)
  }
  const revert = () => {
    const before = baseline.current ?? color
    if (baseline.current) onPreview?.(before)
    baseline.current = null
    setLive(null)
    setDraft(before)
  }
  const at = (lch: Lch) => [lchToHex(lch.l, lch.c, lch.h), lch] as const
  const fromPad = (event: PointerEvent<HTMLCanvasElement>) => {
    const rect = event.currentTarget.getBoundingClientRect()
    return at({ l: 100 * (1 - clamp((event.clientY - rect.top) / rect.height)), c: MAX_CHROMA * clamp((event.clientX - rect.left) / rect.width), h: position.h })
  }
  const fromHue = (event: PointerEvent<HTMLCanvasElement>) => {
    const rect = event.currentTarget.getBoundingClientRect()
    return at({ ...position, h: 360 * clamp((event.clientY - rect.top) / rect.height) })
  }
  const drag = (pick: (event: PointerEvent<HTMLCanvasElement>) => readonly [string, Lch]) => ({
    onPointerDown: (event: PointerEvent<HTMLCanvasElement>) => { event.currentTarget.setPointerCapture?.(event.pointerId); preview(...pick(event)) },
    onPointerMove: (event: PointerEvent<HTMLCanvasElement>) => { if (event.currentTarget.hasPointerCapture?.(event.pointerId)) preview(...pick(event)) },
    onPointerUp: (event: PointerEvent<HTMLCanvasElement>) => { event.currentTarget.releasePointerCapture?.(event.pointerId); commit(...pick(event)) },
  })
  const nudge = (event: KeyboardEvent<HTMLCanvasElement>, next: Lch | null) => {
    if (!next) return
    event.preventDefault()
    commit(...at(next))
  }
  const typed = (value: string) => { const hex = normalizeHex(value); return hex ? [hex, lchOf(hex, hue)] as const : null }
  useEffect(() => paintPad(padRef.current, position.h), [position.h])
  useEffect(() => paintHueStrip(hueRef.current), [])
  return <div className="csp-custom">
    <div className="csp-custom-value" data-note={note ? true : undefined}>
      <button aria-label={`${t('Selected color')} ${shown}`} className="csp-swatch csp-current" style={{ '--palette-color': shown } as CSSProperties} type="button"><FlowGlyph height={9} name="swatchCheck" width={10}/></button>
      <label>
        <span>HEX</span>
        <input aria-label={t('HEX color')} maxLength={7} onBlur={() => { const next = typed(draft); if (next) commit(...next); else revert() }} onChange={event => { setDraft(event.target.value); const next = typed(event.target.value); if (next && event.target.value.trim().length >= 6) preview(...next) }} onKeyDown={event => { if (event.key !== 'Enter') return; event.preventDefault(); const next = typed(draft); if (next) commit(...next) }} spellCheck={false} value={draft}/>
      </label>
      {note && <span className="csp-custom-note">{note}</span>}
    </div>
    <div className="csp-custom-controls">
      <div className="csp-pad">
        <canvas aria-label={t('Saturation and brightness')} aria-valuemax={100} aria-valuemin={0} aria-valuenow={Math.round(position.l)} height={100} onKeyDown={event => {
          const step = ({ ArrowLeft: [0, -1], ArrowRight: [0, 1], ArrowDown: [-1, 0], ArrowUp: [1, 0] } as Record<string, [number, number]>)[event.key]
          nudge(event, step ? { l: Math.min(100, Math.max(0, position.l + step[0])), c: Math.min(MAX_CHROMA, Math.max(0, position.c + step[1])), h: position.h } : null)
        }} ref={padRef} role="slider" tabIndex={-1} width={350} {...drag(fromPad)}/>
        <i style={{ background: hexToDisplayP3(shown), left: `${Math.min(1, position.c / MAX_CHROMA) * 100}%`, top: `${100 - position.l}%` }}/>
      </div>
      <div className="csp-hue">
        <canvas aria-label={t('Hue')} aria-valuemax={360} aria-valuemin={0} aria-valuenow={Math.round(position.h)} height={100} onKeyDown={event => {
          const delta = event.key === 'ArrowUp' ? -2 : event.key === 'ArrowDown' ? 2 : 0
          nudge(event, delta ? { ...position, h: (position.h + delta + 360) % 360 } : null)
        }} ref={hueRef} role="slider" tabIndex={-1} width={6} {...drag(fromHue)}/>
        <i style={{ top: `${(position.h / 360) * 100}%` }}/>
      </div>
    </div>
  </div>
}

/**
 * Paints the lightness × chroma pad for one hue in 2px blocks's canvas: lightness
 * 100 → 0 down, chroma 0 → 132 across.
 */
function paintPad(canvas: HTMLCanvasElement | null, hue: number) {
  const context = canvas?.getContext('2d')
  if (!canvas || !context) return
  const { width, height } = canvas
  const image = context.createImageData(width, height)
  for (let y = 0; y < height; y += 2) {
    for (let x = 0; x < width; x += 2) {
      const [r, g, b] = lchToRgb(100 - (y / height) * 100, (x / width) * MAX_CHROMA, hue)
      for (let dy = 0; dy < 2 && y + dy < height; dy++) for (let dx = 0; dx < 2 && x + dx < width; dx++) {
        const offset = ((y + dy) * width + x + dx) * 4
        image.data[offset] = r; image.data[offset + 1] = g; image.data[offset + 2] = b; image.data[offset + 3] = 255
      }
    }
  }
  context.putImageData(image, 0, 0)
}

function paintHueStrip(canvas: HTMLCanvasElement | null) {
  const context = canvas?.getContext('2d')
  if (!canvas || !context) return
  const { width, height } = canvas
  const image = context.createImageData(width, height)
  for (let y = 0; y < height; y++) {
    const [r, g, b] = lchToRgb(HUE_STRIP_LIGHTNESS, HUE_STRIP_CHROMA, (y / height) * 360)
    for (let x = 0; x < width; x++) {
      const offset = (y * width + x) * 4
      image.data[offset] = r; image.data[offset + 1] = g; image.data[offset + 2] = b; image.data[offset + 3] = 255
    }
  }
  context.putImageData(image, 0, 0)
}

/** `#abc`, `abc`, `#aabbcc` or `aabbcc` → `#aabbcc`; anything else → null. */
function normalizeHex(value: string): string | null {
  const trimmed = value.trim()
  const short = /^#?([0-9a-f]{3})$/i.exec(trimmed)
  if (short) return `#${short[1].split('').map(part => part + part).join('').toLowerCase()}`
  const full = /^#?([0-9a-f]{6})$/i.exec(trimmed)
  return full ? `#${full[1].toLowerCase()}` : null
}

function clamp(value: number) { return Math.min(1, Math.max(0, value)) }
