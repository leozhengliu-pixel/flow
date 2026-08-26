import type { SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement>

export { FilterIcon, DisplayIcon, ChevronRightIcon } from '@/components/ui/view-action-icons'

export function DetailsIcon({ open = false, ...props }: IconProps & { open?: boolean }) {
  return <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" {...props}><path fill="currentColor" fillRule="evenodd" d="M4.25 2A3.25 3.25 0 0 0 1 5.25v5.5A3.25 3.25 0 0 0 4.25 14h7.5A3.25 3.25 0 0 0 15 10.75v-5.5A3.25 3.25 0 0 0 11.75 2h-7.5ZM2.5 5.5a2 2 0 0 1 2-2h7a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2h-7a2 2 0 0 1-2-2v-5Z"/><rect x={open ? 8 : 10} y="5" width={open ? 3.5 : 1.5} height="6" rx=".75" fill="currentColor" style={{ transition: 'x 250ms, width 250ms' }}/></svg>
}

export function SearchIcon(props: IconProps) {
  return <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true" {...props}><circle cx="7" cy="7" r="4.75" fill="none" stroke="currentColor" strokeWidth="1.5"/><path d="m10.5 10.5 3 3" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="1.5"/></svg>
}
