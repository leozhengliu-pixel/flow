import { useId } from 'react'

/** The ridgeline cut through Flow's mark, in the mark's 48-unit drawing space. Shared with public/favicon.svg. */
export const FLOW_LOGO_RIDGE = 'M0 33C11 33 14 13 24 13C31 13 33 26 37 26C40.5 26 42.5 20 48 17'

/**
 * Flow's mark: a rounded tile with a one-stroke ridgeline cut through it. Draws in currentColor.
 * `outline` draws it as a hairline drawing instead (Linear's faint background mark): each piece is filled with
 * `--flow-logo-outline-fill` and edged with a 0.5px `--flow-logo-outline-stroke`, both set by the host.
 */
export function FlowLogo({ className, size, title, variant = 'solid' }: { className?: string; size?: number; title?: string; variant?: 'solid' | 'outline' }) {
  const id = `flow-logo-${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`
  const label = { role: title ? 'img' : undefined, 'aria-label': title, 'aria-hidden': title ? undefined : true } as const
  if (variant === 'outline') return <svg className={className} width={size} height={size} viewBox="4 4 40 40" fill="none" overflow="visible" {...label}>
    <mask id={`${id}-cut`} maskUnits="userSpaceOnUse" x="0" y="0" width="48" height="48">
      <rect width="48" height="48" fill="#fff"/>
      <path d={FLOW_LOGO_RIDGE} fill="none" stroke="#000" strokeWidth="5"/>
    </mask>
    {/* The band's two edges: a slightly wider ridge stroke with the band itself (and anything off the tile) cut away. */}
    <mask id={`${id}-edges`} maskUnits="userSpaceOnUse" x="0" y="0" width="48" height="48">
      <rect x="4" y="4" width="40" height="40" rx="10" fill="#fff"/>
      <path d={FLOW_LOGO_RIDGE} fill="none" stroke="#000" strokeWidth="5"/>
    </mask>
    <rect x="4" y="4" width="40" height="40" rx="10" mask={`url(#${id}-cut)`} strokeWidth="0.5" vectorEffect="non-scaling-stroke" style={{ fill: 'var(--flow-logo-outline-fill)', stroke: 'var(--flow-logo-outline-stroke)' }}/>
    <path d={FLOW_LOGO_RIDGE} mask={`url(#${id}-edges)`} strokeWidth="5.12" style={{ stroke: 'var(--flow-logo-outline-stroke)' }}/>
  </svg>
  return <svg className={className} width={size} height={size} viewBox="4 4 40 40" fill="currentColor" {...label}>
    <mask id={id} maskUnits="userSpaceOnUse" x="0" y="0" width="48" height="48">
      <rect width="48" height="48" fill="#fff"/>
      <path d={FLOW_LOGO_RIDGE} fill="none" stroke="#000" strokeWidth="5"/>
    </mask>
    <rect x="4" y="4" width="40" height="40" rx="10" mask={`url(#${id})`}/>
  </svg>
}
