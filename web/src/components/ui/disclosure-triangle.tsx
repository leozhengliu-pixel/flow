/**
 * Linear's 16px disclosure triangle. It points right when collapsed; pass
 * `open` to rotate it down (Linear rotates the same glyph 90deg).
 */
export function DisclosureTriangle({ className, open = false }: { className?: string; open?: boolean }) {
  return <svg aria-hidden="true" className={['flow-disclosure-triangle', className].filter(Boolean).join(' ')} data-open={open || undefined} fill="currentColor" style={{ transform: open ? 'rotate(90deg)' : undefined, transition: 'transform 150ms ease' }} height="16" viewBox="0 0 16 16" width="16"><path d="M7.00194 10.6239C6.66861 10.8183 6.25 10.5779 6.25 10.192V5.80802C6.25 5.42212 6.66861 5.18169 7.00194 5.37613L10.7596 7.56811C11.0904 7.76105 11.0904 8.23895 10.7596 8.43189L7.00194 10.6239Z"/></svg>
}
