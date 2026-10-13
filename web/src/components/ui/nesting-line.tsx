import './nesting-line.css'

/** The reference app's NestingLine curve (10×9): a 1px stem that bends right into the nested row. */
const CURVE_PATH = 'M0 0h1v1c0 2.5 2.212 3.546 2.212 3.546L9.737 8.06c.568.306.094 1.186-.474.88l-6.48-3.488S0 4 0 1V0Z'

/**
 * The reference app's NestingLine: a 1px vertical line `heightAbove + heightBelow` tall whose foot curves right,
 * hanging a row under its parent (milestone under project, delegated agent under assignee, a nested
 * menu row). The curve's bottom sits `heightBelow - 8`px from the line's bottom. Callers position it
 * (absolute) with a class; the colour is the reference app's `bgBorderSolid` (`--nesting-line`).
 */
export function NestingLine({ heightAbove = 0, heightBelow = 0, className }: { heightAbove?: number; heightBelow?: number; className?: string }) {
  const height = heightAbove + heightBelow
  return <span aria-hidden className={className ? `nesting-line ${className}` : 'nesting-line'} data-nesting-line="" style={height > 0 ? { height } : undefined}>
    <svg className="nesting-line__curve" height="9" viewBox="0 0 10 9" width="10" style={{ bottom: heightBelow - 8 }}><path d={CURVE_PATH}/></svg>
  </span>
}
