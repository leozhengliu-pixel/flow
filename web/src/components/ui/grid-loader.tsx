import { useLayoutEffect, useRef, useState, useSyncExternalStore } from 'react'
import { cn } from '@/lib/utils'
import './grid-loader.css'

import { GRID_LOADER_FRAMES, type GridLoaderVariant } from './grid-loader-frames'

export type { GridLoaderVariant }

const GRID = 5
const DOT_RADIUS = 1
const PITCH = 3.5
const DOT_CENTRES = Array.from({ length: GRID * GRID }, (_, index) => ({ cx: 1 + (index % GRID) * PITCH, cy: 1 + Math.floor(index / GRID) * PITCH }))
const isLit = (mask: number, index: number) => Math.floor(mask / 2 ** index) % 2 === 1

// ---- sprite strip -----------------------------------------------------------------------------

const spriteCache = new Map<string, string>()
const canvasAvailable = () => typeof document !== 'undefined' && typeof navigator !== 'undefined' && !/jsdom/i.test(navigator.userAgent)

function renderSprite(variant: GridLoaderVariant, color: string, dimColor: string, scale: number) {
  const key = `${variant}|${color}|${dimColor}|${scale}`
  const cached = spriteCache.get(key)
  if (cached) return cached
  if (!canvasAvailable()) return undefined
  const frames = GRID_LOADER_FRAMES[variant]
  const canvas = document.createElement('canvas')
  canvas.width = frames.length * 16 * scale
  canvas.height = 16 * scale
  const context = canvas.getContext('2d')
  if (!context) return undefined
  context.scale(scale, scale)
  frames.forEach((mask, frame) => {
    const dot = (index: number) => {
      context.beginPath()
      context.arc(frame * 16 + DOT_CENTRES[index].cx, DOT_CENTRES[index].cy, DOT_RADIUS, 0, Math.PI * 2)
      context.fill()
    }
    context.globalAlpha = 0.3
    context.fillStyle = dimColor
    DOT_CENTRES.forEach((_, index) => dot(index))
    context.globalAlpha = 1
    context.fillStyle = color
    DOT_CENTRES.forEach((_, index) => { if (isLit(mask, index)) dot(index) })
  })
  const url = canvas.toDataURL('image/png')
  spriteCache.set(key, url)
  return url
}

// ---- shared observers ---------------------------------------------------------------------------

let themeEpoch = 0
const themeListeners = new Set<() => void>()
let themeObserver: MutationObserver | undefined
let colorSchemeQuery: MediaQueryList | undefined
const bumpTheme = () => { themeEpoch += 1; themeListeners.forEach(listener => listener()) }

function subscribeTheme(listener: () => void) {
  themeListeners.add(listener)
  if (!themeObserver && typeof MutationObserver !== 'undefined' && typeof document !== 'undefined') {
    themeObserver = new MutationObserver(bumpTheme)
    themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'class', 'style'] })
    colorSchemeQuery = typeof window.matchMedia === 'function' ? window.matchMedia('(prefers-color-scheme: dark)') : undefined
    colorSchemeQuery?.addEventListener?.('change', bumpTheme)
  }
  return () => {
    themeListeners.delete(listener)
    if (themeListeners.size) return
    themeObserver?.disconnect()
    themeObserver = undefined
    colorSchemeQuery?.removeEventListener?.('change', bumpTheme)
    colorSchemeQuery = undefined
  }
}
const getThemeEpoch = () => themeEpoch

const visibilityCallbacks = new WeakMap<Element, (visible: boolean) => void>()
let visibilityObserver: IntersectionObserver | undefined
function observeVisibility(element: Element, callback: (visible: boolean) => void) {
  if (typeof IntersectionObserver === 'undefined') return () => undefined
  visibilityObserver ??= new IntersectionObserver(entries => entries.forEach(entry => visibilityCallbacks.get(entry.target)?.(entry.isIntersecting)))
  visibilityCallbacks.set(element, callback)
  visibilityObserver.observe(element)
  return () => { visibilityCallbacks.delete(element); visibilityObserver?.unobserve(element) }
}

// ---- component ----------------------------------------------------------------------------------

export type GridLoaderProps = {
  variant?: GridLoaderVariant
  size?: number
  /** Milliseconds per frame. */
  interval?: number
  /** Lit-dot colour; defaults to the host's current text colour. */
  color?: string
  /** Colour of the 30% alpha unlit dots; defaults to `color`. */
  dimColor?: string
  initialFrame?: number
  className?: string
  /** Accessible name. Without it the loader is decorative (aria-hidden). */
  label?: string
}

export function GridLoader({ variant = 'scope', size = 16, interval = 200, color, dimColor, initialFrame, className, label }: GridLoaderProps) {
  const frames = GRID_LOADER_FRAMES[variant] ?? GRID_LOADER_FRAMES.scope
  const frameCount = frames.length
  const svgRef = useRef<SVGSVGElement>(null)
  const dimRef = useRef<SVGGElement>(null)
  const [sprite, setSprite] = useState<string>()
  const theme = useSyncExternalStore(subscribeTheme, getThemeEpoch, getThemeEpoch) // bumps (re-resolving colours) on theme change
  const startFrame = ((Math.trunc(initialFrame ?? 0) % frameCount) + frameCount) % frameCount
  const scale = Math.max(2, Math.ceil(size / 16 * 2))

  // The canvas needs concrete colours, so resolve them from the host after layout (and again when the theme flips).
  useLayoutEffect(() => {
    const svg = svgRef.current
    if (!svg || !canvasAvailable()) return
    const resolved = getComputedStyle(svg).color
    const dim = dimColor && dimRef.current ? getComputedStyle(dimRef.current).color : resolved
    setSprite(renderSprite(variant, resolved, dim, scale))
  }, [variant, scale, color, dimColor, className, theme])

  useLayoutEffect(() => {
    const svg = svgRef.current
    if (!svg || frameCount < 2) return
    return observeVisibility(svg, visible => { if (visible) svg.removeAttribute('data-paused'); else svg.setAttribute('data-paused', '') })
  }, [frameCount])

  const mask = frames[startFrame]
  const animated = frameCount > 1

  return <svg
    ref={svgRef}
    className={cn('gli', className)}
    width={size}
    height={size}
    viewBox="0 0 16 16"
    data-variant={variant}
    data-frames={frameCount}
    data-sprite={sprite ? '' : undefined}
    style={color ? { color } : undefined}
    role={label ? 'status' : undefined}
    aria-label={label}
    aria-hidden={label ? undefined : true}
    focusable="false"
  >
    {dimColor && <g ref={dimRef} display="none" style={{ color: dimColor }} />}
    <g className="gli-static">
      {DOT_CENTRES.map((dot, index) => {
        const lit = isLit(mask, index)
        return <circle key={index} cx={dot.cx} cy={dot.cy} r={DOT_RADIUS} fill="currentColor" opacity={lit ? 1 : 0.3} data-lit={lit ? 'true' : 'false'} style={!lit && dimColor ? { color: dimColor } : undefined} />
      })}
    </g>
    {sprite && <image
      className="gli-sprite"
      href={sprite}
      x={0}
      y={0}
      width={frameCount * 16}
      height={16}
      style={animated ? { animationDuration: `${frameCount * interval}ms`, animationTimingFunction: `steps(${frameCount})`, animationDelay: `${-startFrame * interval}ms` } : undefined}
    />}
  </svg>
}
