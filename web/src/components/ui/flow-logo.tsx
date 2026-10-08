import { useId } from 'react'

/** The ridgeline cut through Flow's mark, in the mark's 48-unit drawing space. Shared with public/favicon.svg. */
export const FLOW_LOGO_RIDGE = 'M0 33C11 33 14 13 24 13C31 13 33 26 37 26C40.5 26 42.5 20 48 17'

/** Flow's mark: a rounded tile with a one-stroke ridgeline cut through it. Draws in currentColor. */
export function FlowLogo({ className, size, title }: { className?: string; size?: number; title?: string }) {
  const maskId = `flow-logo-${useId().replace(/[^a-zA-Z0-9_-]/g, '')}`
  return <svg className={className} width={size} height={size} viewBox="4 4 40 40" fill="currentColor" role={title ? 'img' : undefined} aria-label={title} aria-hidden={title ? undefined : true}>
    <mask id={maskId} maskUnits="userSpaceOnUse" x="0" y="0" width="48" height="48">
      <rect width="48" height="48" fill="#fff"/>
      <path d={FLOW_LOGO_RIDGE} fill="none" stroke="#000" strokeWidth="5"/>
    </mask>
    <rect x="4" y="4" width="40" height="40" rx="10" mask={`url(#${maskId})`}/>
  </svg>
}
