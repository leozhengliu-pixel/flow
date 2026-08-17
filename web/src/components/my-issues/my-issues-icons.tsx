import type { SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement>

export function FilterIcon(props: IconProps) {
  return <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" {...props}><path fill="currentColor" fillRule="evenodd" d="M14.25 3a.75.75 0 0 1 0 1.5H1.75a.75.75 0 0 1 0-1.5h12.5ZM4 8a.75.75 0 0 1 .75-.75h6.5a.75.75 0 0 1 0 1.5h-6.5A.75.75 0 0 1 4 8Zm2.75 3.5a.75.75 0 0 0 0 1.5h2.5a.75.75 0 0 0 0-1.5h-2.5Z"/></svg>
}

export function DisplayIcon(props: IconProps) {
  return <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" {...props}><path fill="currentColor" fillRule="evenodd" d="M7 2.5c1.119 0 2.066.736 2.385 1.75h5.365a.75.75 0 0 1 0 1.5H9.385A2.501 2.501 0 0 1 4.615 5.75H2.25a.75.75 0 0 1 0-1.5h2.365A2.501 2.501 0 0 1 7 2.5ZM7 4a1 1 0 1 0 0 2 1 1 0 0 0 0-2Zm3 9.5a2.501 2.501 0 0 1-2.385-1.75H2.25a.75.75 0 0 1 0-1.5h5.365a2.501 2.501 0 0 1 4.77 0h2.365a.75.75 0 0 1 0 1.5h-2.365A2.501 2.501 0 0 1 10 13.5Zm0-1.5a1 1 0 1 0 0-2 1 1 0 0 0 0 2Z"/></svg>
}

export function DetailsIcon({ open = false, ...props }: IconProps & { open?: boolean }) {
  return <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" {...props}><path fill="currentColor" fillRule="evenodd" d="M4.25 2A3.25 3.25 0 0 0 1 5.25v5.5A3.25 3.25 0 0 0 4.25 14h7.5A3.25 3.25 0 0 0 15 10.75v-5.5A3.25 3.25 0 0 0 11.75 2h-7.5ZM2.5 5.5a2 2 0 0 1 2-2h7a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2h-7a2 2 0 0 1-2-2v-5Z"/><rect x={open ? 8 : 10} y="5" width={open ? 3.5 : 1.5} height="6" rx=".75" fill="currentColor" style={{ transition: 'x 250ms, width 250ms' }}/></svg>
}

export function ChevronRightIcon(props: IconProps) {
  return <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" {...props}><path d="m4.5 2.5 3.5 3.5-3.5 3.5" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.25"/></svg>
}

export function SearchIcon(props: IconProps) {
  return <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true" {...props}><circle cx="7" cy="7" r="4.75" fill="none" stroke="currentColor" strokeWidth="1.5"/><path d="m10.5 10.5 3 3" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="1.5"/></svg>
}
