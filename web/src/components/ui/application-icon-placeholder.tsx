import { Fragment } from 'react'

import './application-icon-placeholder.css'

/** The default application icon (the reference app's concentric-circle grid) for applications without a logo. */
export function ApplicationIconPlaceholder() {
  return <svg viewBox="0 0 56 56" aria-hidden="true" className="oauth-icon-placeholder">
    {[14, 28, 42].map(value => <Fragment key={value}><path d={`M${value} 0V56`}/><path d={`M0 ${value}H56`}/></Fragment>)}
    <path d="M0 0L56 56M56 0L0 56"/>
    <circle cx="28" cy="28" r="20"/><circle cx="28" cy="28" r="14"/><circle cx="28" cy="28" r="8"/>
  </svg>
}
