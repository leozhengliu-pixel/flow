import type { TeamSettings } from '@/types/flow'
import { estimateIconLevel, estimateLabel, estimateShortLabel } from '@/lib/estimates'
import './estimate-icon.css'

export type EstimateSettings = Partial<Pick<TeamSettings, 'estimateType' | 'estimateAllowZero' | 'estimateExtended'>> | undefined

// Triangle with its apex at (8, 2.5) and a 12.5px base on y = 13.5; the fill is
// split into three bands that light up from the bottom as the estimate grows.
const APEX_Y = 2.5
const BASE_Y = 13.5
const HALF_BASE = 6.25
const BANDS: [number, number][] = [[10.35, 13.5], [6.55, 9.6], [APEX_Y, 5.8]]

function halfWidth(y: number) { return HALF_BASE * (y - APEX_Y) / (BASE_Y - APEX_Y) }
function band([top, bottom]: [number, number]) {
  const t = halfWidth(top), b = halfWidth(bottom)
  const point = (x: number, y: number) => `${Math.round(x * 100) / 100},${y}`
  return [point(8 - t, top), point(8 + t, top), point(8 + b, bottom), point(8 - b, bottom)].join(' ')
}
const BAND_POINTS = BANDS.map(band)

/**
 * The estimate glyph: a triangle outline whose bands fill with the size of the
 * estimate. `none` draws the dashed outline used for "No estimate".
 */
export function EstimateIcon({ level = 0, none = false, size = 16, className }: { level?: 0 | 1 | 2 | 3; none?: boolean; size?: number; className?: string }) {
  return <svg aria-hidden="true" focusable="false" className={`estimate-icon${className ? ` ${className}` : ''}`} data-level={none ? 'none' : level} width={size} height={size} viewBox="0 0 16 16" fill="none">
    <path d="M8 2.5 14.25 13.5H1.75Z" stroke="currentColor" strokeWidth="1.2" strokeLinejoin="round" strokeDasharray={none ? '2 1.6' : undefined}/>
    {!none && BAND_POINTS.slice(0, level).map(points => <polygon key={points} points={points} fill="currentColor"/>)}
  </svg>
}

/** Icon for an issue's estimate value on its team's scale (dashed when unestimated). */
export function EstimateValueIcon({ value, settings, size }: { value?: number | null; settings: EstimateSettings; size?: number }) {
  const unset = value === undefined || value === null || value < 0
  return <EstimateIcon none={unset} level={unset ? 0 : estimateIconLevel(value, settings)} size={size}/>
}

/** "△ 8" chip used on list rows, board cards and group headers. */
export function EstimateChip({ value, settings, className, label }: { value: number; settings: EstimateSettings; className?: string; label?: string }) {
  return <span className={`estimate-chip${className ? ` ${className}` : ''}`} aria-label={label ?? `Estimate ${estimateLabel(value, settings?.estimateType ?? 'notUsed')}`}>
    <EstimateValueIcon value={value} settings={settings} size={14}/>
    <span className="estimate-chip__value">{estimateShortLabel(value, settings?.estimateType ?? 'notUsed')}</span>
  </span>
}

