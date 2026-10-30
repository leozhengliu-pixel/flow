import type { TeamSettings } from '@/types/flow'
import { estimateLabel, estimateShortLabel } from '@/lib/estimates'
import './estimate-icon.css'

export type EstimateSettings = Partial<Pick<TeamSettings, 'estimateType' | 'estimateAllowZero' | 'estimateExtended'>> | undefined

const ESTIMATE_PATH = 'M3.741 14.5h8.521c1.691 0 2.778-1.795 1.993-3.293l-4.26-8.134c-.842-1.608-3.144-1.608-3.986 0l-4.26 8.134C.962 12.705 2.05 14.5 3.74 14.5ZM8 3.368a.742.742 0 0 0-.663.402l-4.26 8.134A.75.75 0 0 0 3.741 13H8V3.367Z'
const NO_ESTIMATE_PATH = 'M8.666 3.77a.75.75 0 0 0-1.329 0L6.033 6.26l-1.329-.696 1.305-2.49c.842-1.609 3.144-1.609 3.986 0l1.305 2.49-1.33.696-1.304-2.49ZM9.545 13v1.5h2.717c1.691 0 2.778-1.795 1.993-3.293l-1.304-2.49-1.329.695 1.305 2.49a.75.75 0 0 1-.665 1.099H9.545ZM4.382 9.413l-1.33-.696-1.304 2.49c-.785 1.499.302 3.295 1.993 3.295H6.46V13H3.74a.75.75 0 0 1-.664-1.098l1.305-2.49Z'

/**
 * All estimates share the filled-left triangle. Only "No estimate" menu rows
 * use the broken outline.
 */
export function EstimateIcon({ none = false, size = 16, className }: { none?: boolean; size?: number; className?: string }) {
  return <svg aria-hidden="true" focusable="false" className={`estimate-icon${className ? ` ${className}` : ''}`} width={size} height={size} viewBox="0 0 16 16" fill="currentColor">
    <path d={none ? NO_ESTIMATE_PATH : ESTIMATE_PATH} fill="currentColor" fillRule="evenodd"/>
  </svg>
}

/** Menu option icon; property triggers always use EstimateIcon directly. */
export function EstimateValueIcon({ value, size }: { value?: number | null; size?: number }) {
  const unset = value === undefined || value === null || value < 0
  return <EstimateIcon none={unset} size={size}/>
}

const BOARD_ESTIMATE_ARC = 'M6.95588 5.28329L10.6901 7.43926C11.0235 7.63171 11.0235 8.11283 10.6901 8.30528L6.95588 10.4612C6.62255 10.6537 6.20588 10.4131 6.20588 10.0282L6.20588 5.71631C6.20588 5.33141 6.62255 5.09084 6.95588 5.28329Z'

/** The reference board card's estimate glyph: a play inside a progress ring. */
export function BoardEstimateIcon({ size = 14 }: { size?: number }) {
  return <svg aria-hidden="true" focusable="false" className="estimate-icon board-estimate-icon" width={size} height={size} viewBox="0 0 16 16" fill="currentColor">
    <path d={BOARD_ESTIMATE_ARC}/>
    <circle cx="8" cy="8" r="6.25" fill="none" stroke="currentColor" strokeWidth="1.5" strokeDasharray="39.269908169872416" strokeDashoffset="5.357544031041707" strokeLinecap="round" transform="rotate(-54.26075591453578 6.25 6.25)" style={{ transformBox: 'fill-box' }}/>
    <circle cx="7.6" cy="2.1" r="1.5" fill="var(--display-modified-dot)"/>
  </svg>
}

/** "△ 8" chip used on list rows, board cards and group headers. */
export function EstimateChip({ value, settings, className, label, board = false }: { value: number; settings: EstimateSettings; className?: string; label?: string; board?: boolean }) {
  return <span className={`estimate-chip${board ? ' estimate-chip--board' : ''}${className ? ` ${className}` : ''}`} aria-label={label ?? `Estimate ${estimateLabel(value, settings?.estimateType ?? 'notUsed')}`}>
    {board ? <><EstimateIcon size={14}/><BoardEstimateIcon size={14}/></> : <EstimateIcon size={14}/>}
    <span className="estimate-chip__value">{estimateShortLabel(value, settings?.estimateType ?? 'notUsed')}</span>
  </span>
}
